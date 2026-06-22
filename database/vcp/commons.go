package database

import (
	"context"
	"reflect"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// ErroredResource updates the state of a resource to "error" and sets the error message.
func (d *DataStoreRepository) ErroredResource(ctx context.Context, resource interface{}, errMessage string) (interface{}, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	t := reflect.TypeOf(resource)
	if t.Kind() != reflect.Pointer {
		return nil, vsaerrors.New("resource must be a pointer to a struct")
	}
	s := reflect.ValueOf(resource).Elem()
	if s.Kind() == reflect.Struct {
		uuid := s.FieldByName("UUID").String()
		f := s.FieldByName("State")
		if !f.IsValid() {
			return nil, vsaerrors.New("State field not found in the errored resource")
		}
		f.SetString(datamodel.LifeCycleStateError)
		f = s.FieldByName("StateDetails")
		if !f.IsValid() {
			return nil, vsaerrors.New("StateDetails field not found in the errored resource")
		}
		f.SetString(errMessage)

		model := reflect.New(t).Elem().Interface()
		if err = tx.Model(model).
			Where("uuid = ?", uuid).
			Updates(resource).Error; err != nil {
			return nil, err
		}
		return resource, nil
	}
	return nil, vsaerrors.New("invalid resource type for ErroredResource method, expected a struct")
}
