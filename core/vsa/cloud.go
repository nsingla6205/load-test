package vsa

import (
	"github.com/go-openapi/strfmt"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

func (rc *OntapRestProvider) CloudTargetCreate(name, containerName, accessKey, secretKey string) (*ontapRest.CloudTarget, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	params := &ontapRest.CloudTargetCreateParams{
		Name:      &name,
		Container: &containerName,
	}
	if accessKey != "" && secretKey != "" {
		params.AccessKey = &accessKey
		secret := strfmt.Password(secretKey)
		params.SecretPassword = &secret
	}
	_, job, err := client.Cloud().CloudTargetCreate(params)
	if err != nil {
		return nil, err
	}
	err = waitForJobIfNeeded(rc, job)
	if err != nil {
		return nil, err
	}
	cloudTarget, err := client.Cloud().CloudTargetGet(&name)
	if err != nil {
		return nil, err
	}
	return cloudTarget, nil
}

func (rc *OntapRestProvider) CloudTargetGet(name *string) (*ontapRest.CloudTarget, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	cloudTarget, err := client.Cloud().CloudTargetGet(name)
	if err != nil {
		return nil, err
	}
	if cloudTarget == nil {
		return nil, errors.New("invalid CloudTarget response from API")
	}
	// Return the created SVM
	return cloudTarget, nil
}

func (rc *OntapRestProvider) CloudTargetDelete(uuid string) (*OntapAsyncResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	_, job, err := client.Cloud().CloudTargetDelete(&ontapRest.CloudTargetDeleteParams{
		UUID: uuid,
	})
	if err != nil {
		return nil, err
	}
	if job != nil {
		return &OntapAsyncResponse{JobUUID: job.JobUUID}, err
	}
	return nil, nil
}
