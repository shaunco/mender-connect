// Copyright 2021 Northern.tech AS
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//        http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.

package filetransfer

import (
	"errors"
	"math"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/mendersoftware/mender-connect/config"
	"github.com/mendersoftware/mender-connect/session/model"
	"github.com/mendersoftware/mender-connect/utils"
)

var (
	ErrChrootViolation          = errors.New("the target file path is outside chroot")
	ErrFileOwnerMismatch        = errors.New("the file owner does not match")
	ErrFileGroupMismatch        = errors.New("the file group does not match")
	ErrFollowLinksForbidden     = errors.New("forbidden to follow the link")
	ErrForbiddenToOverwriteFile = errors.New("forbidden to overwrite the file")
	ErrFileTooBig               = errors.New("the file size is over the limit")
	ErrSuidModeForbidden        = errors.New("the set uid mode is forbidden")
	ErrTxBytesLimitExhausted    = errors.New("transmitted bytes limit exhausted")
	ErrOnlyRegularFilesAllowed  = errors.New("only regular files are allowed")
)

var (
	countersUpdateSleepTimeS = 5
)

type Counters struct {
	bytesTransferred           uint64
	bytesReceived              uint64
	rateTransferredLastMinute  float64
	rateReceivedLastMinute     float64
	bytesTransferredLast1W     float64
	bytesReceivedLast1W        float64
	bytesTransferredLastUpdate time.Time
	bytesReceivedLastUpdate    time.Time
	period                     uint64
}

type Permit struct {
	limits   config.Limits
	counters Counters
	// mutex to protect the writes and reads of the counters
	countersMutex *sync.Mutex
}

var countersMutex = &sync.Mutex{}
var deviceCounters = Counters{
	bytesTransferred:           0,
	bytesReceived:              0,
	bytesTransferredLastUpdate: time.Now(),
	bytesReceivedLastUpdate:    time.Now(),
	period:                     0,
}
var counterUpdateRunning = false
var counterUpdateStarted = make(chan bool, 1)

func NewPermit(config config.Limits) *Permit {
	countersMutex.Lock()
	defer countersMutex.Unlock()
	go updatePerHourCounters()
	<-counterUpdateStarted
	return &Permit{
		limits: config,
		counters: Counters{
			bytesTransferred:           0,
			bytesReceived:              0,
			bytesTransferredLastUpdate: time.Now().UTC(),
			bytesReceivedLastUpdate:    time.Now().UTC(),
		},
		// mutex to protect the writes and reads of the Counters
		countersMutex: &sync.Mutex{},
	}
}

func (p *Permit) UploadFile(fileStat model.FileInfo) error {
	if !p.limits.Enabled {
		return nil
	}

	filePath := *fileStat.Path

	//this one actually does nothing, since at the moment of writing,
	//InitFileUpload does not get the size of the file upfront,
	//so this potentially can work once the remote sends the size
	if p.limits.FileTransfer.MaxFileSize > 0 &&
		fileStat.Size != nil &&
		uint64(*fileStat.Size) > p.limits.FileTransfer.MaxFileSize {
		return ErrFileTooBig
	}

	if !utils.IsInChroot(filePath, p.limits.FileTransfer.Chroot) {
		return ErrChrootViolation
	}

	if !p.limits.FileTransfer.FollowSymLinks {
		absolutePath, err := filepath.EvalSymlinks(path.Dir(filePath))
		if err != nil {
			return err
		} else {
			if absolutePath != path.Dir(filePath) {
				return ErrFollowLinksForbidden
			}
		}
	}

	if !p.limits.FileTransfer.AllowOverwrite && utils.FileExists(filePath) {
		return ErrForbiddenToOverwriteFile
	}

	if p.limits.FileTransfer.AllowOverwrite && utils.FileExists(filePath) {
		if !utils.FileOwnerMatches(filePath, p.limits.FileTransfer.OwnerPut) {
			return ErrFileOwnerMismatch
		}

		if !utils.FileGroupMatches(filePath, p.limits.FileTransfer.GroupPut) {
			return ErrFileGroupMismatch
		}
	}

	if !p.limits.FileTransfer.AllowSuid &&
		fileStat.Mode != nil &&
		(os.FileMode(*fileStat.Mode)&os.ModeSetuid) != 0 {
		return ErrSuidModeForbidden
	}

	return nil
}

func (p *Permit) DownloadFile(fileStat model.FileInfo) error {
	if !p.limits.Enabled {
		return nil
	}

	filePath := *fileStat.Path

	if p.limits.FileTransfer.RegularFilesOnly && !utils.IsRegularFile(filePath) {
		return ErrOnlyRegularFilesAllowed
	}

	if !utils.IsInChroot(filePath, p.limits.FileTransfer.Chroot) {
		return ErrChrootViolation
	}

	for _, owner := range p.limits.FileTransfer.OwnerGet {
		if utils.FileOwnerMatches(filePath, owner) {
			break
		}
		return ErrFileOwnerMismatch
	}

	for _, group := range p.limits.FileTransfer.GroupGet {
		if utils.FileGroupMatches(filePath, group) {
			break
		}
		return ErrFileGroupMismatch
	}

	if !p.limits.FileTransfer.FollowSymLinks {
		absolutePath, err := filepath.EvalSymlinks(filePath)
		if err != nil {
			return err
		} else {
			if absolutePath != filePath {
				return ErrFollowLinksForbidden
			}
		}
	}

	if p.limits.FileTransfer.MaxFileSize > 0 {
		fileSize := utils.FileSize(filePath)
		if fileSize > 0 && p.limits.FileTransfer.MaxFileSize < uint64(fileSize) {
			return ErrFileTooBig
		}
	}

	return nil
}

func (p *Permit) BytesSent(n uint64) (belowLimit bool) {
	if !p.limits.Enabled {
		return true
	}

	countersMutex.Lock()
	defer countersMutex.Unlock()

	belowLimit = true
	if n != 0 {
		if deviceCounters.bytesTransferred < math.MaxUint64-n {
			deviceCounters.bytesTransferred += n
		}
	}
	if p.limits.FileTransfer.Counters.MaxBytesTxPerMinute > 0 &&
		uint64(deviceCounters.bytesTransferredLast1W) >= p.limits.FileTransfer.Counters.MaxBytesTxPerMinute {
		belowLimit = false
	}

	p.countersMutex.Lock()
	defer p.countersMutex.Unlock()
	if n != 0 {
		if p.counters.bytesTransferred < math.MaxUint64-n {
			p.counters.bytesTransferred += n
		}
	}
	return belowLimit
}

func (p *Permit) BytesReceived(n uint64) (belowLimit bool) {
	if !p.limits.Enabled {
		return true
	}

	countersMutex.Lock()
	defer countersMutex.Unlock()

	belowLimit = true
	if n != 0 {
		if deviceCounters.bytesReceived < math.MaxUint64-n {
			deviceCounters.bytesReceived += n
		}
	}
	if p.limits.FileTransfer.Counters.MaxBytesRxPerMinute > 0 &&
		uint64(deviceCounters.bytesReceivedLast1W) >= p.limits.FileTransfer.Counters.MaxBytesRxPerMinute {
		belowLimit = false
	}

	p.countersMutex.Lock()
	defer p.countersMutex.Unlock()
	if n != 0 {
		if p.counters.bytesReceived < math.MaxUint64-n {
			p.counters.bytesReceived += n
		}
	}
	return belowLimit
}

func (p *Permit) BelowMaxAllowedFileSize(offset int64) (belowLimit bool) {
	if !p.limits.Enabled {
		return true
	}

	if offset < 0 {
		return true
	}
	if p.limits.FileTransfer.MaxFileSize > 0 &&
		uint64(offset) >= p.limits.FileTransfer.MaxFileSize {
		return false
	} else {
		return true
	}
}

func (p *Permit) PreserveModes(path string, mode os.FileMode) error {
	if !p.limits.Enabled {
		return nil
	}

	if (mode & syscall.S_ISUID) != 0 {
		mode &= os.ModePerm
		if p.limits.FileTransfer.Umask != "" {
			umask, err := strconv.ParseUint(p.limits.FileTransfer.Umask, 8, 32)
			if err != nil {
				return err
			}

			mode = os.ModePerm ^ os.FileMode(uint32(os.ModePerm)&uint32(umask))
		}
		mode |= os.ModeSetuid
	} else {
		if p.limits.FileTransfer.Umask != "" {
			umask, err := strconv.ParseUint(p.limits.FileTransfer.Umask, 8, 32)
			if err != nil {
				return err
			}

			mode = os.ModePerm ^ os.FileMode(uint32(os.ModePerm)&uint32(umask))
		}
		mode &= os.ModePerm
	}

	if p.limits.FileTransfer.PreserveMode {
		return os.Chmod(path, mode)
	} else {
		return nil
	}
}

func (p *Permit) PreserveOwnerGroup(path string, uid int, gid int) error {
	if !p.limits.Enabled {
		return nil
	}

	forcedSet := false
	if p.limits.FileTransfer.OwnerPut != "" {
		u, err := user.Lookup(p.limits.FileTransfer.OwnerPut)
		if err != nil {
			return err
		}
		uid, err = strconv.Atoi(u.Uid)
		if err != nil {
			return err
		}
		forcedSet = true
	}
	if p.limits.FileTransfer.GroupPut != "" {
		u, err := user.LookupGroup(p.limits.FileTransfer.GroupPut)
		if err != nil {
			return err
		}
		gid, err = strconv.Atoi(u.Gid)
		if err != nil {
			return err
		}
		forcedSet = true
	}
	if forcedSet {
		return os.Chown(path, uid, gid)
	}
	if p.limits.FileTransfer.PreserveOwner {
		return os.Chown(path, uid, gid)
	} else {
		return nil
	}
}

func updatePerHourCounters() {
	const one60th = 0.016666666666666666
	if counterUpdateRunning {
		counterUpdateStarted <- false
		return
	}

	counterUpdateRunning = true
	counterUpdateStarted <- true
	//exponential decrease for "avg" over 1 minute: \exp(-dt[s]/(1*60[s]))
	expWeight1m := math.Exp(-float64(countersUpdateSleepTimeS) * one60th) // / 60.0)
	//analogy to the uptime weighted avg
	//expWeight5m := math.Exp(-float64(countersUpdateSleepTimeS) * 0.003333333333333333)  // / (5*60.0))
	//expWeight15m := math.Exp(-float64(countersUpdateSleepTimeS) * 0.001111111111111111) // / (15*60.0))
	deviceCounters.bytesReceivedLast1W = 0.0
	deviceCounters.bytesTransferredLast1W = 0.0

	for counterUpdateRunning {
		bytesRXLastMinute0 := deviceCounters.bytesReceived
		bytesTXLastMinute0 := deviceCounters.bytesTransferred
		bytesRXLastMinute := uint64(0)
		bytesTXLastMinute := uint64(0)
		for i := 0; i < int(int64(time.Minute)/int64(countersUpdateSleepTimeS)/int64(time.Second)); i++ {
			time.Sleep(time.Duration(countersUpdateSleepTimeS) * time.Second)
			bytesRXLastMinute += deviceCounters.bytesReceived - bytesRXLastMinute0
			bytesTXLastMinute += deviceCounters.bytesTransferred - bytesTXLastMinute0
			deviceCounters.bytesReceivedLast1W = expWeight1m*deviceCounters.bytesReceivedLast1W +
				float64(bytesRXLastMinute) - expWeight1m*float64(bytesRXLastMinute)
			deviceCounters.bytesTransferredLast1W = expWeight1m*deviceCounters.bytesTransferredLast1W +
				float64(bytesTXLastMinute) - expWeight1m*float64(bytesTXLastMinute)
		}
		deviceCounters.rateTransferredLastMinute = float64(bytesTXLastMinute) * one60th
		deviceCounters.rateReceivedLastMinute = float64(bytesRXLastMinute) * one60th
	}
}

func GetCounters() (uint64, uint64, float64, float64, float64, float64) {
	countersMutex.Lock()
	defer countersMutex.Unlock()

	return deviceCounters.bytesTransferred,
		deviceCounters.bytesReceived,
		deviceCounters.rateTransferredLastMinute,
		deviceCounters.rateReceivedLastMinute,
		deviceCounters.bytesTransferredLast1W,
		deviceCounters.bytesReceivedLast1W
}