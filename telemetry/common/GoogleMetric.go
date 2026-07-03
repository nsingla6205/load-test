package common

import (
	"encoding/json"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/entity"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// GoogleType represents the type of Google metric.
type GoogleType int

// GoogleMetric represents a Google metric.
type GoogleMetric struct {
	Record interface{}
}

// InvalidGoogleMetricException represents an exception for invalid Google metrics.
type InvalidGoogleMetricException struct {
	msg string
}

const (
	BillingMetric  GoogleType = iota // Represents a Google billing metric.
	HydratedMetric                   // Represents a hydrated Google metric.
)

// NewInvalidGoogleMetricException creates a new InvalidGoogleMetricException with the given message.
// Parameters:
// - msg: The message describing the exception.
// Returns:
// - A pointer to the newly created InvalidGoogleMetricException.
func NewInvalidGoogleMetricException(msg string) *InvalidGoogleMetricException {
	return &InvalidGoogleMetricException{msg: msg}
}

// Error returns the error message for the InvalidGoogleMetricException.
func (e *InvalidGoogleMetricException) Error() string {
	return e.msg
}

// NewGoogleMetric creates a new GoogleMetric with the given record.
// Parameters:
// - record: The record representing the Google metric.
// Returns:
// - A pointer to the newly created GoogleMetric.
func NewGoogleMetric(record interface{}) *GoogleMetric {
	return &GoogleMetric{Record: record}
}

// GetCustomerId returns the customer ID for the Google metric.
// Returns:
// - The customer ID as a string.
// - An error if the customer ID could not be retrieved.
func (gm *GoogleMetric) GetCustomerId() (string, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return "", err
		}
		if metric.VendorCustomerID == nil {
			return "", NewInvalidGoogleMetricException("AccountName is nil in UsageBillingMetric")
		}
		return *metric.VendorCustomerID, nil
	case HydratedMetric:
		metric, err := gm.GetAsHydratedMetric()
		if err != nil {
			return "", err
		}
		if metric.Metadata.AccountName == nil {
			return "", NewInvalidGoogleMetricException("AccountName is nil in HydratedMetric")
		}
		return *metric.Metadata.AccountName, nil
	}
	return "", NewInvalidGoogleMetricException("Invalid GoogleMetric type")
}

// GetResourceName returns the resource name for the Google metric.
// Returns:
// - The resource name as a string.
// - An error if the resource name could not be retrieved.
func (gm *GoogleMetric) GetResourceName() (string, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return "", err
		}
		if metric.ResourceName == nil {
			return "", NewInvalidGoogleMetricException("AccountName is nil in UsageBillingMetric")
		}
		return *metric.ResourceName, nil
	case HydratedMetric:
		metric, err := gm.GetAsHydratedMetric()
		if err != nil {
			return "", err
		}
		if metric.Metadata.ResourceDisplayName != nil {
			return *metric.Metadata.ResourceDisplayName, nil
		}
		return "", errors.New("ResourceDisplayName is nil")
	default:
		return "", NewInvalidGoogleMetricException("Invalid GoogleMetric type")
	}
}

// GetMeasuredType returns the measured type for the Google metric.
// Returns:
// - The measured type.
// - An error if the measured type could not be retrieved.
func (gm *GoogleMetric) GetMeasuredType() (metadata.MeasuredType, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return "", err
		}
		return metadata.MeasuredTypeFromValue(metric.MeasuredType), nil
	case HydratedMetric:
		metric, err := gm.GetAsHydratedMetric()
		if err != nil {
			return "", err
		}
		return metric.MeasuredType, nil
	default:
		return "", NewInvalidGoogleMetricException("Invalid GoogleMetric type")
	}
}

// GetQuantity returns the quantity for the Google metric.
// Returns:
// - The quantity as an int64.
// - An error if the quantity could not be retrieved.
func (gm *GoogleMetric) GetQuantity() (int64, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return 0, err
		}
		return int64(metric.Quantity), nil
	case HydratedMetric:
		metric, err := gm.GetAsHydratedMetric()
		if err != nil {
			return 0, err
		}
		return int64(metric.Quantity), nil
	default:
		return 0, NewInvalidGoogleMetricException("Invalid GoogleMetric type")
	}
}

// GetDoubleQuantity returns the double quantity for the Google metric.
// Returns:
// - The double quantity as a float64.
// - An error if the double quantity could not be retrieved.
func (gm *GoogleMetric) GetDoubleQuantity() (float64, error) {
	if gm.GetType() == HydratedMetric {
		metric, err := gm.GetAsHydratedMetric()
		if err != nil {
			return 0, err
		}
		return metric.Quantity, nil
	}
	return 0, NewInvalidGoogleMetricException("Only hydrated metrics have a double-valued quantity")
}

// GetStringQuantity returns the string quantity for the Google metric.
// Returns:
// - The string quantity.
// - An error if the string quantity could not be retrieved.
func (gm *GoogleMetric) GetStringQuantity() (string, error) {
	return "", NewInvalidGoogleMetricException("Invalid GoogleMetric type")
}

// GetResourceType returns the resource type for the Google metric.
// Returns:
// - The resource type.
// - An error if the resource type could not be retrieved.
func (gm *GoogleMetric) GetResourceType() (metadata.ResourceType, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return "", err
		}
		return metadata.ResourceTypeFromValue(metric.ResourceType), nil
	case HydratedMetric:
		metric, err := gm.GetAsHydratedMetric()
		if err != nil {
			return "", err
		}
		return metric.Metadata.ResourceType, nil
	default:
		return "", NewInvalidGoogleMetricException("Invalid GoogleMetric type")
	}
}

// GetRegion returns the region for the Google metric.
// Returns:
// - The region as a string.
// - An error if the region could not be retrieved.
func (gm *GoogleMetric) GetRegion() (string, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return "", err
		}
		if metric.RegionName == nil {
			return "", nil
		}
		return *metric.RegionName, nil
	case HydratedMetric:
		metric, err := gm.GetAsHydratedMetric()
		if err != nil {
			return "", err
		}
		if metric.Metadata.RegionName != nil {
			return *metric.Metadata.RegionName, nil
		}
		return "", nil
	default:
		return "", NewInvalidGoogleMetricException("Invalid GoogleMetric type")
	}
}

// GetZone returns the zone for the Google metric.
// This is populated only for AT billing metrics on zonal pools.
// Returns:
// - The zone as a string (empty if not set).
// - An error if the zone could not be retrieved.
func (gm *GoogleMetric) GetZone() (string, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return "", err
		}
		if metric.Zone == nil {
			return "", nil
		}
		return *metric.Zone, nil
	case HydratedMetric:
		return "", nil
	default:
		return "", NewInvalidGoogleMetricException("Invalid GoogleMetric type")
	}
}

// GetTags returns the tags for the Google metric.
// Returns:
// - The tags as a string.
// - An error if the tags could not be retrieved.
func (gm *GoogleMetric) GetTags() (string, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return "", err
		}
		if metric.BillingLabels != nil {
			return *metric.BillingLabels, nil
		}
		return "", nil
	case HydratedMetric:
		return "", nil
	default:
		return "", NewInvalidGoogleMetricException("Only billing metrics have tag information")
	}
}

// GetAsUsageBillingMetric converts the Google metric to a Usage billing metric.
// Returns:
// - A pointer to the Usage billing metric.
// - An error if the conversion fails.
func (gm *GoogleMetric) GetAsUsageBillingMetric() (*datamodel.AggregatedUsage, error) {
	if gm.Record == nil {
		return nil, errors.New("record is nil")
	}
	mapping, ok := gm.Record.(*datamodel.AggregatedUsage)
	if !ok {
		return nil, errors.New("invalid type conversion")
	}
	return mapping, nil
}

// GetAsHydratedMetric converts the Google metric to a Hydrated metric.
// Returns:
// - A pointer to the Hydrated metric.
// - An error if the conversion fails.
func (gm *GoogleMetric) GetAsHydratedMetric() (*entity.HydratedMetric, error) {
	if gm.Record == nil {
		return nil, errors.New("record is nil")
	}
	metric, ok := gm.Record.(*entity.HydratedMetric)
	if !ok {
		return nil, errors.New("invalid type conversion")
	}
	return metric, nil
}

// GetType returns the type of the Google metric.
// Returns:
// - The Google metric type.
func (gm *GoogleMetric) GetType() GoogleType {
	switch gm.Record.(type) {
	case *datamodel.AggregatedUsage:
		return BillingMetric
	case *entity.HydratedMetric:
		return HydratedMetric
	default:
		return -1 // or any other default value indicating an unknown type
	}
}

// Validate checks for missing fields in the Google metric.
// Returns:
// - A slice of strings representing the missing fields.
func (gm *GoogleMetric) Validate() []string {
	var missingFields []string

	if customerId, err := gm.GetCustomerId(); err != nil || customerId == "" {
		missingFields = append(missingFields, "customerId")
	}
	if measuredType, err := gm.GetMeasuredType(); err != nil || measuredType == "" {
		missingFields = append(missingFields, "measuredType")
	}
	if _, err := gm.GetQuantity(); err != nil {
		missingFields = append(missingFields, "quantity")
	}
	if gm.GetType() == -1 {
		missingFields = append(missingFields, "type")
	}
	return missingFields
}

// GetStartTime returns the start time for the Google metric.
// Returns:
// - The start time as an int64.
// - An error if the start time could not be retrieved.
func (gm *GoogleMetric) GetStartTime() (int64, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return 0, err
		}
		return metric.AggregationStart.Unix(), nil
	case HydratedMetric:
		metric, err := gm.GetAsHydratedMetric()
		if err != nil {
			return 0, err
		}
		return metric.Timestamp.ToTime().Unix(), nil
	default:
		return 0, NewInvalidGoogleMetricException("Invalid GoogleMetric type")
	}
}

// GetEndTime returns the end time for the Google metric.
// Returns:
// - The end time as an int64.
// - An error if the end time could not be retrieved.
func (gm *GoogleMetric) GetEndTime() (int64, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return 0, err
		}
		return metric.AggregationEnd.Unix(), nil
	case HydratedMetric:
		metric, err := gm.GetAsHydratedMetric()
		if err != nil {
			return 0, err
		}
		return metric.Timestamp.ToTime().Unix(), nil
	default:
		return 0, NewInvalidGoogleMetricException("Invalid GoogleMetric type")
	}
}

// GetLabels  returns the user labels for the Google metric.
// Returns:
// - The user labels as a map[string]string.
// - An error if the user labels could not be retrieved.
func (gm *GoogleMetric) GetLabels() (map[string]string, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return nil, err
		}

		if metric.BillingLabels == nil {
			return nil, nil
		}
		var labels map[string]string
		err = json.Unmarshal([]byte(*metric.BillingLabels), &labels)
		if err != nil {
			return nil, err
		}
		return labels, nil
	case HydratedMetric:
		return nil, nil
	default:
		return nil, NewInvalidGoogleMetricException("Invalid GoogleMetric type")
	}
}

// GetResourceUUID Returns:
// - The UUID of the resource as a string.
// - An error if the UUID could not be retrieved.
func (gm *GoogleMetric) GetResourceUUID() (string, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return "", err
		}
		if metric.ResourceUUID == "" {
			return "", nil
		}
		return metric.ResourceUUID, nil
	case HydratedMetric:
		metric, err := gm.GetAsHydratedMetric()
		if err != nil {
			return "", err
		}
		if metric.Metadata.ResourceUUID == nil {
			return "", nil
		}
		return *metric.Metadata.ResourceUUID, nil
	default:
		return "", NewInvalidGoogleMetricException("Invalid GoogleMetric type")
	}
}

// GetServiceLevel returns the service level for the Google metric.
// Returns:
// - The service level as a string.
// - An error if the service level could not be retrieved.
func (gm *GoogleMetric) GetServiceLevel() (string, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return "", err
		}
		return metric.ServiceLevel, nil
	default:
		return "", NewInvalidGoogleMetricException("Invalid GoogleMetric type")
	}
}

// GetReplicationType returns the replication type for replication metrics.
// Returns:
// - The replication type as a string.
// - An error if the replication type could not be retrieved.
func (gm *GoogleMetric) GetReplicationType() (string, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return "", err
		}
		return metric.ReplicationType, nil
	default:
		return "", NewInvalidGoogleMetricException("Invalid GoogleMetric type")
	}
}

// GetSourceRegion returns the source region for replication metrics.
// Returns:
// - The source region as a string.
// - An error if the source region could not be retrieved.
func (gm *GoogleMetric) GetSourceRegion() (string, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return "", err
		}
		if metric.SourceRegion != nil {
			return *metric.SourceRegion, nil
		}
		return "", nil
	default:
		return "", NewInvalidGoogleMetricException("Invalid GoogleMetric type")
	}
}

// GetDestinationRegion returns the destination region for replication metrics.
// Returns:
// - The destination region as a string.
// - An error if the destination region could not be retrieved.
func (gm *GoogleMetric) GetDestinationRegion() (string, error) {
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return "", err
		}
		if metric.DestinationRegion != nil {
			return *metric.DestinationRegion, nil
		}
		return "", nil
	default:
		return "", NewInvalidGoogleMetricException("Invalid GoogleMetric type")
	}
}

func (gm *GoogleMetric) GetBillingMode() (string, error) {
	if gm.Record == nil {
		return "", errors.New("record is nil")
	}
	switch gm.GetType() {
	case BillingMetric:
		metric, err := gm.GetAsUsageBillingMetric()
		if err != nil {
			return "", err
		}
		if metric.BillingMode != "" {
			return string(metric.BillingMode), nil
		}
		return "", nil
	default:
		return "", NewInvalidGoogleMetricException("Invalid GoogleMetric type")
	}
}
