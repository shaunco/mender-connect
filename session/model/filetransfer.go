package model

import (
	validation "github.com/go-ozzo/ozzo-validation/v4"
	wsft "github.com/mendersoftware/go-lib-micro/ws/filetransfer"
)

type FileInfo wsft.FileInfo

func (f FileInfo) Validate() error {
	return validation.ValidateStruct(&f,
		validation.Field(&f.Path, validation.Required),
	)
}

type StatFile wsft.StatFile

func (s StatFile) Validate() error {
	return validation.ValidateStruct(&s,
		validation.Field(&s.Path, validation.Required),
	)
}

type GetFile wsft.GetFile

func (f GetFile) Validate() error {
	return validation.ValidateStruct(&f,
		validation.Field(&f.Path, validation.Required),
	)
}
