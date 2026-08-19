package vcm

import "errors"

var (
	ErrJobNotFound      = errors.New("vcm job not found")
	ErrJobAlreadyExists = errors.New("vcm job already exists")
)
