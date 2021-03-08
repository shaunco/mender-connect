package filetransfer

import (
	"io/ioutil"
	"math"
	"math/rand"
	"os"
	"os/user"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mendersoftware/mender-connect/config"
	"github.com/mendersoftware/mender-connect/session/model"
)

func TestGetCounters(t *testing.T) {
	rand.Seed(time.Now().UnixNano())

	initTX := rand.Uint64()
	initRX := rand.Uint64()
	initTXRate := rand.Float64()
	initRXRate := rand.Float64()
	deviceCountersLastH.bytesTransferred = initTX
	deviceCountersLastH.bytesReceived = initRX
	deviceCountersLastH.currentRxRate = initRXRate
	deviceCountersLastH.currentTxRate = initTXRate

	time.Sleep(8 * time.Second)
	tx, rx, txRate, rxRate := GetCounters()
	assert.Equal(t, initTX, tx)
	assert.Equal(t, initRX, rx)
	assert.True(t, math.Abs(initTXRate-txRate) <= 0.001)
	assert.True(t, math.Abs(initRXRate-rxRate) <= 0.001)
}

func TestUpdatePerHourCounters(t *testing.T) {
	deviceCountersLastH = Counters{
		bytesTransferred:           0,
		bytesReceived:              0,
		bytesTransferredLastUpdate: time.Now(),
		bytesReceivedLastUpdate:    time.Now(),
		period:                     0,
	}
	countersUpdateSleepTimeS = time.Second

	NewPermit(config.Limits{})
	NewPermit(config.Limits{})
	NewPermit(config.Limits{})
	NewPermit(config.Limits{})
	p := NewPermit(config.Limits{
		Enabled: true,
		FileTransfer: config.FileTransferLimits{
			Chroot:         "",
			FollowSymLinks: false,
			AllowOverwrite: false,
			OwnerPut:       "",
			OwnerGet:       "",
			Umask:          "",
			MaxFileSize:    0,
			Counters: config.Counters{
				MaxBytesTxPerHour: 0,
				MaxBytesRxPerHour: 0,
			},
			AllowSuid:          false,
			RegularFilesOnly:   false,
			DoNotPreserveMode:  false,
			DoNotPreserveOwner: false,
		},
	})
	thread1BytesSent := []uint64{
		1024,
		1024,
		1024,
		1024,
		1024,
		1024,
		1024,
		1024,
	}
	thread2BytesReceived := []uint64{
		1024,
		1024,
		1024,
		1024,
		1024,
		1024,
		1024,
		1024,
	}
	thread2BytesSent := []uint64{
		2048,
		2048,
		2048,
		2048,
		2048,
		2048,
		2048,
		2048,
	}
	thread1BytesReceived := []uint64{
		2048,
		2048,
		2048,
		2048,
		2048,
		2048,
		2048,
		2048,
	}
	totalBytesReceivedRateExpected := float64(0.0)
	totalBytesSentRateExpected := float64(0.0)
	totalBytesReceivedExpected := uint64(0)
	for _, b := range thread1BytesReceived {
		totalBytesReceivedExpected += b
	}
	for _, b := range thread2BytesReceived {
		totalBytesReceivedExpected += b
	}
	totalBytesSentExpected := uint64(0)
	for _, b := range thread1BytesSent {
		totalBytesSentExpected += b
	}
	for _, b := range thread2BytesSent {
		totalBytesSentExpected += b
	}
	go func() {
		i := 7
		for i >= 0 {
			time.Sleep(50 * time.Millisecond)
			p.BytesSent(thread1BytesSent[i])
			p.BytesReceived(thread1BytesReceived[i])
			i--
		}
	}()
	go func() {
		i := 7
		for i >= 0 {
			time.Sleep(50 * time.Millisecond)
			p.BytesSent(thread2BytesSent[i])
			p.BytesReceived(thread2BytesReceived[i])
			i--
		}
	}()
	counterUpdateRunning = false
	time.Sleep(6 * time.Second)
	totalBytesReceivedRateExpected = float64(totalBytesReceivedExpected) / float64(deviceCountersLastH.period)
	totalBytesSentRateExpected = float64(totalBytesSentExpected) / float64(deviceCountersLastH.period)
	t.Logf("expected rates: tx/rx rates: %.2f/%.2f counters:%+v",
		totalBytesReceivedRateExpected,
		totalBytesSentRateExpected,
		deviceCountersLastH)
	assert.True(t, math.Abs(totalBytesSentRateExpected-deviceCountersLastH.currentTxRate) < 0.0001)
	assert.True(t, math.Abs(totalBytesReceivedRateExpected-deviceCountersLastH.currentRxRate) < 0.0001)
	time.Sleep(2 * time.Second)
	assert.Equal(t, totalBytesSentExpected, deviceCountersLastH.bytesTransferred)
	assert.Equal(t, totalBytesReceivedExpected, deviceCountersLastH.bytesReceived)
	//check that now the updatePerHourCounters should not be running, so after 2s the deviceCountersLastH rates should stay the same
}

func createRandomFile(prefix string) string {
	if prefix != "" {
		prefix = os.TempDir() + prefix
		os.Mkdir(prefix, 0755)
	}

	f, err := ioutil.TempFile(prefix, "")
	if err != nil || f == nil {
		return ""
	}
	defer f.Close()
	fileName := f.Name()

	rand.Seed(time.Now().UnixNano())

	maxBytes := 512
	array := make([]byte, rand.Intn(maxBytes))
	for i, _ := range array {
		array[i] = byte(rand.Intn(255))
	}
	f.Write(array)
	f.Close()
	return fileName
}

func TestPermit_PreserveOwnerGroup(t *testing.T) {
	fileName := createRandomFile("")
	if fileName == "" {
		t.Fatal("cant create a file")
	}
	defer os.Remove(fileName)

	u, err := user.Current()
	if err != nil {
		t.Fatal("cant get current user")
	}

	counterUpdateRunning = true //disables the counters update routine
	p := NewPermit(config.Limits{
		Enabled: true,
		FileTransfer: config.FileTransferLimits{
			Chroot:         "",
			FollowSymLinks: false,
			AllowOverwrite: false,
			OwnerPut:       "",
			OwnerGet:       "",
			Umask:          "",
			MaxFileSize:    0,
			Counters: config.Counters{
				MaxBytesTxPerHour: 0,
				MaxBytesRxPerHour: 0,
			},
			AllowSuid:          false,
			RegularFilesOnly:   false,
			DoNotPreserveMode:  false,
			DoNotPreserveOwner: false,
		},
	})

	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	err = p.PreserveOwnerGroup(fileName, uid, gid)
	assert.NoError(t, err)

	stat, err := os.Stat(fileName)
	if err != nil {
		t.Fatal("cant get file stats")
	}
	var statT *syscall.Stat_t
	var ok bool

	if statT, ok = stat.Sys().(*syscall.Stat_t); !ok {
		t.Fatal("cant get file stats")
	}

	assert.Equal(t, uint32(uid), statT.Uid)
	assert.Equal(t, uint32(gid), statT.Gid)
}

func TestPermit_PreserveModes(t *testing.T) {
	fileName := createRandomFile("")
	if fileName == "" {
		t.Fatal("cant create a file")
	}
	defer os.Remove(fileName)

	counterUpdateRunning = true //disables the counters update routine
	p := NewPermit(config.Limits{
		Enabled: true,
		FileTransfer: config.FileTransferLimits{
			Chroot:         "",
			FollowSymLinks: false,
			AllowOverwrite: false,
			OwnerPut:       "",
			OwnerGet:       "",
			Umask:          "",
			MaxFileSize:    0,
			Counters: config.Counters{
				MaxBytesTxPerHour: 0,
				MaxBytesRxPerHour: 0,
			},
			AllowSuid:          false,
			RegularFilesOnly:   false,
			DoNotPreserveMode:  false,
			DoNotPreserveOwner: false,
		},
	})

	testCases := []struct {
		Name         string
		Umask        string
		Mode         string
		ExpectedMode string
	}{
		{
			Name:         "owner-group-other mode",
			Mode:         "755",
			ExpectedMode: "755",
		},
		{
			Name:         "owner-group-other mode +s",
			Mode:         "4755",
			ExpectedMode: "4755",
		},
		{
			Name:         "owner-group-other mode with umask",
			Umask:        "0202",
			ExpectedMode: "575",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			p.limits.FileTransfer.Umask = tc.Umask
			os.Chmod(fileName, 0)
			mode, _ := strconv.ParseUint(tc.Mode, 8, 32)
			p.PreserveModes(fileName, os.FileMode(mode))

			stat, err := os.Stat(fileName)
			if err != nil {
				t.Fatal("cant get file stats")
			}

			actualMode := stat.Mode()
			if (stat.Mode() & os.ModeSetuid) != 0 {
				actualMode &= os.ModePerm
				actualMode |= S_ISUID
			} else {
				actualMode &= os.ModePerm
			}

			expectedMode, _ := strconv.ParseUint(tc.ExpectedMode, 8, 32)
			expectedMode &= 07777
			assert.Equal(t, os.FileMode(expectedMode), actualMode)
		})
	}
}

func TestPermit_BelowMaxAllowedFileSize(t *testing.T) {
	p := NewPermit(config.Limits{
		Enabled: true,
		FileTransfer: config.FileTransferLimits{
			Chroot:         "",
			FollowSymLinks: false,
			AllowOverwrite: false,
			OwnerPut:       "",
			OwnerGet:       "",
			Umask:          "",
			MaxFileSize:    0,
			Counters: config.Counters{
				MaxBytesTxPerHour: 0,
				MaxBytesRxPerHour: 0,
			},
			AllowSuid:          false,
			RegularFilesOnly:   false,
			DoNotPreserveMode:  false,
			DoNotPreserveOwner: false,
		},
	})

	testCases := []struct {
		Name               string
		Offset             int64
		MaxAllowedFileSize uint64
		ExpectedBelow      bool
	}{
		{
			Name:               "below the limit",
			Offset:             1024,
			MaxAllowedFileSize: 4096,
			ExpectedBelow:      true,
		},
		{
			Name:               "over the limit",
			Offset:             8192,
			MaxAllowedFileSize: 4096,
			ExpectedBelow:      false,
		},
		{
			Name:               "at the limit",
			Offset:             4096,
			MaxAllowedFileSize: 4096,
			ExpectedBelow:      false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			p.limits.FileTransfer.MaxFileSize = tc.MaxAllowedFileSize
			assert.Equal(t, tc.ExpectedBelow, p.BelowMaxAllowedFileSize(tc.Offset))
		})
	}
}

func TestPermit_DownloadFile(t *testing.T) {
	testCases := []struct {
		Name             string
		Permit           *Permit
		ExpectedDownload error
	}{
		{
			Name: "not a regular file",
			Permit: NewPermit(config.Limits{
				Enabled: true,
				FileTransfer: config.FileTransferLimits{
					RegularFilesOnly: true,
				},
			}),
			ExpectedDownload: ErrOnlyRegularFilesAllowed,
		},
		{
			Name: "not in a chroot",
			Permit: NewPermit(config.Limits{
				Enabled: true,
				FileTransfer: config.FileTransferLimits{
					Chroot: "/var/chroot/mender/file_transfer",
				},
			}),
			ExpectedDownload: ErrChrootViolation,
		},
	}

	path := "/root/file.bin"
	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			err := tc.Permit.DownloadFile(model.FileInfo{
				Path: &path,
			})
			if tc.ExpectedDownload != nil {
				assert.EqualError(t, err, tc.ExpectedDownload.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
