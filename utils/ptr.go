package utils

// PtrBoolEqual reports whether two optional bool pointers hold the same value.
func PtrBoolEqual(currentValue, updateValue *bool) bool {
	if currentValue == nil && updateValue == nil {
		return true
	}
	if currentValue == nil || updateValue == nil {
		return false
	}
	return *currentValue == *updateValue
}

// PtrInt64Equal reports whether two optional int64 pointers hold the same value.
func PtrInt64Equal(currentValue, updateValue *int64) bool {
	if currentValue == nil && updateValue == nil {
		return true
	}
	if currentValue == nil || updateValue == nil {
		return false
	}
	return *currentValue == *updateValue
}
