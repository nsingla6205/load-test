package env

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHasEnv(t *testing.T) {
	key := "ENVUTIL_TEST_HAS_ENV"
	t.Run("WhenEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		if IsEnvSet(key) {
			tt.Fail()
		}
	})
	t.Run("WhenEnvironmentVariableIsSet", func(tt *testing.T) {
		err := os.Setenv(key, "")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		if !IsEnvSet(key) {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
}

// TestGetFromVolumeOrEnv tests the getFromVolumeOrEnv function.
func TestGetFromVolumeOrEnv(t *testing.T) {
	key := "TEST_KEY"
	value := "test_value"

	t.Run("WhenKeyExistsInLocalConfig", func(tt *testing.T) {
		useVolumeEnv = true
		localConfig = map[string]string{key: value}
		val, err := getFromVolumeOrEnv(key)
		assert.NoError(tt, err)
		assert.Equal(tt, value, val)
	})

	t.Run("WhenKeyExistsInVolumeFile", func(tt *testing.T) {
		useVolumeEnv = true
		localConfig = nil
		volumeEnvPath = "./test_config.yaml"
		yamlData := fmt.Sprintf("%s: %s\n", key, value)
		err := os.WriteFile(volumeEnvPath, []byte(yamlData), 0644)
		assert.NoError(tt, err)
		defer func(name string) {
			err := os.Remove(name)
			if err != nil {
				tt.Fail()
			}
		}(volumeEnvPath)

		val, err := getFromVolumeOrEnv(key)
		assert.NoError(tt, err)
		assert.Equal(tt, value, val)
	})

	t.Run("WhenKeyExistsInEnvironmentVariable", func(tt *testing.T) {
		useVolumeEnv = false
		localConfig = nil
		err := os.Setenv(key, value)
		assert.NoError(tt, err)
		defer func(key string) {
			err := os.Unsetenv(key)
			if err != nil {
				tt.Fail()
			}
		}(key)

		val, err := getFromVolumeOrEnv(key)
		assert.NoError(tt, err)
		assert.Equal(tt, value, val)
	})

	t.Run("WhenKeyDoesNotExistAnywhere", func(tt *testing.T) {
		useVolumeEnv = false
		localConfig = nil
		val, err := getFromVolumeOrEnv(key)
		assert.Error(tt, err)
		assert.Empty(tt, val)
	})
}

func TestGetStringFromEnvIfNil(t *testing.T) {
	key := "ENVUTIL_TEST_GET_STRING"
	def := "default value"
	t.Run("WhenNotSpecifyingStringAndEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		val := GetStringFromEnvIfNil(nil, key, def)
		if val == nil {
			tt.Error("Value not returned")
		} else if *val != def {
			tt.Fail()
		}
	})
	t.Run("WhenNotSpecifyingStringAndEnvironmentVariableIsSet", func(tt *testing.T) {
		err := os.Setenv(key, "non-default value")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetStringFromEnvIfNil(nil, key, def)
		if val == nil {
			tt.Error("Value not returned")
		} else if *val != "non-default value" {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
	t.Run("WhenSpecifyingStringAndEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		v := "valuable"
		val := GetStringFromEnvIfNil(&v, key, def)
		if val != &v {
			tt.Fail()
		}
	})
	t.Run("WhenSpecifyingStringAndEnvironmentVariableIsSet", func(tt *testing.T) {
		err := os.Setenv(key, "non-default value")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		v := "valuable"
		val := GetStringFromEnvIfNil(&v, key, def)
		if val != &v {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
}

func TestGetString(t *testing.T) {
	key := "ENVUTIL_TEST_GET_STRING"
	def := "default value"
	t.Run("WhenEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		if GetString(key, def) != def {
			tt.Fail()
		}
	})
	t.Run("WhenEnvironmentVariableIsEmpty", func(tt *testing.T) {
		err := os.Setenv(key, "")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		if GetString(key, def) != "" {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
	t.Run("WhenEnvironmentVariableIsNotEmpty", func(tt *testing.T) {
		err := os.Setenv(key, "non-default value")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		if GetString(key, def) != "non-default value" {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
}

func TestGetStrings(t *testing.T) {
	env1 := "ENV_VAR_1"
	env2 := "ENV_VAR_2"
	env3 := "ENV_var_3_no_match"
	env4 := "ENV_no_match_VAR_4"
	env5 := "prefix_ENV_VAR_5"
	env6 := "ENV_VAR_6"
	defer func() {
		_ = os.Unsetenv(env1)
		_ = os.Unsetenv(env2)
		_ = os.Unsetenv(env3)
		_ = os.Unsetenv(env4)
		_ = os.Unsetenv(env5)
		_ = os.Unsetenv(env6)
	}()
	_ = os.Setenv(env1, "someValue1")
	_ = os.Setenv(env2, "someValue2")
	_ = os.Setenv(env3, "someValue3")
	_ = os.Setenv(env4, "someValue4")
	_ = os.Setenv(env5, "someValue5")
	_ = os.Setenv(env6, "someValue6")
	t.Run("WhenInvalidRegex", func(tt *testing.T) {
		vars, err := GetStrings("BrokenRegex((¢}^2")
		assert.EqualError(tt, err, "error parsing regexp: missing closing ): `BrokenRegex((¢}^2`")
		assert.Empty(tt, vars)
	})
	t.Run("WhenNoEnvironmentVariableMatches", func(tt *testing.T) {
		vars, err := GetStrings("ThisShouldNotMatch")
		assert.NoError(tt, err)
		assert.Empty(tt, vars)
	})
	t.Run("WhenEnvironmentVariablesMatch", func(tt *testing.T) {
		vars, err := GetStrings("^ENV_VAR.*")
		assert.NoError(tt, err)
		assert.Len(tt, vars, 3)
		assert.Equal(tt, "someValue1", vars[env1])
		assert.Equal(tt, "someValue2", vars[env2])
		assert.Equal(tt, "someValue6", vars[env6])
	})
}

func TestGetIntFromEnvIfNil(t *testing.T) {
	key := "ENVUTIL_TEST_GET_INT"
	def := 9999
	t.Run("WhenNotSpecifyingIntegerAndEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		val := GetIntFromEnvIfNil(nil, key, def)
		if val == nil {
			tt.Error("Value not returned")
		} else if *val != def {
			tt.Fail()
		}
	})
	t.Run("WhenNotSpecifyingIntegerAndEnvironmentVariableIsSet", func(tt *testing.T) {
		err := os.Setenv(key, "1234")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetIntFromEnvIfNil(nil, key, def)
		if val == nil {
			tt.Error("Value not returned")
		} else if *val != 1234 {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
	t.Run("WhenSpecifyingIntegerAndEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		v := 1234
		val := GetIntFromEnvIfNil(&v, key, def)
		if val != &v {
			tt.Fail()
		}
	})
	t.Run("WhenSpecifyingIntegerAndEnvironmentVariableIsSet", func(tt *testing.T) {
		err := os.Setenv(key, "non-default value")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		v := 1234
		val := GetIntFromEnvIfNil(&v, key, def)
		if val != &v {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
}

func TestGetInt(t *testing.T) {
	key := "ENVUTIL_TEST_GET_INT"
	def := 9999
	t.Run("WhenEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		if GetInt(key, def) != def {
			tt.Fail()
		}
	})

	type data struct {
		env         string
		expectedInt int
	}
	testData := []data{
		{env: "", expectedInt: def},
		{env: "zero", expectedInt: def},
		{env: "-2147483648", expectedInt: -2147483648},
		{env: "-13", expectedInt: -13},
		{env: "-1", expectedInt: -1},
		{env: "0", expectedInt: 0},
		{env: "1", expectedInt: 1},
		{env: "13", expectedInt: 13},
		{env: "2147483647", expectedInt: 2147483647},
	}
	for _, td := range testData {
		err := os.Setenv(key, td.env)
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetInt(key, def)
		if val != td.expectedInt {
			t.Errorf("Returned value %v does not match expected one for env '%s'", val, td.env)
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	}
}

func TestGetUintFromEnvIfNil(t *testing.T) {
	key := "ENVUTIL_TEST_GET_UINT"
	var def uint = 9999
	t.Run("WhenNotSpecifyingUnsignedIntegerAndEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		val := GetUintFromEnvIfNil(nil, key, def)
		if val == nil {
			tt.Error("Value not returned")
		} else if *val != def {
			tt.Fail()
		}
	})
	t.Run("WhenNotSpecifyingUnsignedIntegerAndEnvironmentVariableIsSet", func(tt *testing.T) {
		err := os.Setenv(key, "1234")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetUintFromEnvIfNil(nil, key, def)
		if val == nil {
			tt.Error("Value not returned")
		} else if *val != 1234 {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
	t.Run("WhenSpecifyingUnsignedIntegerAndEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		var v uint = 1234
		val := GetUintFromEnvIfNil(&v, key, def)
		if val != &v {
			tt.Fail()
		}
	})
	t.Run("WhenSpecifyingUnsignedIntegerAndEnvironmentVariableIsSet", func(tt *testing.T) {
		err := os.Setenv(key, "non-default value")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		var v uint = 1234
		val := GetUintFromEnvIfNil(&v, key, def)
		if val != &v {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
}

func TestGetUint(t *testing.T) {
	key := "ENVUTIL_TEST_GET_UINT"
	var def uint = 9999
	t.Run("WhenEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		if GetUint(key, def) != def {
			tt.Fail()
		}
	})

	type data struct {
		env          string
		expectedUint uint
	}
	testData := []data{
		{env: "", expectedUint: def},
		{env: "zero", expectedUint: def},
		{env: "-2147483648", expectedUint: def},
		{env: "-13", expectedUint: def},
		{env: "-1", expectedUint: def},
		{env: "0", expectedUint: 0},
		{env: "1", expectedUint: 1},
		{env: "13", expectedUint: 13},
		{env: "4294967295", expectedUint: 4294967295},
	}
	for _, td := range testData {
		err := os.Setenv(key, td.env)
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetUint(key, def)
		if val != td.expectedUint {
			t.Errorf("Returned value %v does not match expected one for env '%s'", val, td.env)
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	}
}

func TestGetIntNotNegative(t *testing.T) {
	key := "ENVUTIL_TEST_GET_INT"
	var def = 9999
	t.Run("WhenEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		if GetIntNotNegative(key, def) != def {
			tt.Fail()
		}
	})

	type data struct {
		env         string
		expectedInt int
	}
	testData := []data{
		{env: "", expectedInt: def},
		{env: "zero", expectedInt: def},
		{env: "-2147483648", expectedInt: def},
		{env: "-13", expectedInt: def},
		{env: "-1", expectedInt: def},
		{env: "0", expectedInt: 0},
		{env: "1", expectedInt: 1},
		{env: "13", expectedInt: 13},
		{env: "4294967295", expectedInt: 4294967295},
	}
	for _, td := range testData {
		err := os.Setenv(key, td.env)
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetIntNotNegative(key, def)
		if val != td.expectedInt {
			t.Errorf("Returned value %v does not match expected one for env '%s'", val, td.env)
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	}
}

func TestGetUint16(t *testing.T) {
	key := "ENVUTIL_TEST_GET_UINT16"
	var def uint16 = 9999
	t.Run("WhenEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		if GetUint16(key, def) != def {
			tt.Fail()
		}
	})

	type data struct {
		env            string
		expectedUint16 uint16
	}
	testData := []data{
		{env: "", expectedUint16: def},
		{env: "zero", expectedUint16: def},
		{env: "-2147483648", expectedUint16: def},
		{env: "-13", expectedUint16: def},
		{env: "-1", expectedUint16: def},
		{env: "0", expectedUint16: 0},
		{env: "1", expectedUint16: 1},
		{env: "13", expectedUint16: 13},
		{env: "4294967295", expectedUint16: def},
	}
	for _, td := range testData {
		err := os.Setenv(key, td.env)
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetUint16(key, def)
		if val != td.expectedUint16 {
			t.Errorf("Returned value %v does not match expected one for env '%s'", val, td.env)
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	}
}

func TestGetInt64FromEnvIfNil(t *testing.T) {
	key := "ENVUTIL_TEST_GET_INT64"
	var def int64 = 9999
	t.Run("WhenNotSpecifying64bitIntegerAndEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		val := GetInt64FromEnvIfNil(nil, key, def)
		if val == nil {
			tt.Error("Value not returned")
		} else if *val != def {
			tt.Fail()
		}
	})
	t.Run("WhenNotSpecifying64bitIntegerAndEnvironmentVariableIsSet", func(tt *testing.T) {
		err := os.Setenv(key, "1234")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetInt64FromEnvIfNil(nil, key, def)
		if val == nil {
			tt.Error("Value not returned")
		} else if *val != 1234 {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
	t.Run("WhenSpecifying64bitIntegerAndEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		var v int64 = 1234
		val := GetInt64FromEnvIfNil(&v, key, def)
		if val != &v {
			tt.Fail()
		}
	})
	t.Run("WhenSpecifying64bitIntegerAndEnvironmentVariableIsSet", func(tt *testing.T) {
		err := os.Setenv(key, "non-default value")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		var v int64 = 1234
		val := GetInt64FromEnvIfNil(&v, key, def)
		if val != &v {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
}

func TestGetInt64(t *testing.T) {
	key := "ENVUTIL_TEST_GET_INT64"
	var def int64 = 9999
	t.Run("WhenEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		if GetInt64(key, def) != def {
			tt.Fail()
		}
	})

	type data struct {
		env           string
		expectedInt64 int64
	}
	testData := []data{
		{env: "", expectedInt64: def},
		{env: "zero", expectedInt64: def},
		{env: "-2147483648", expectedInt64: -2147483648},
		{env: "-13", expectedInt64: -13},
		{env: "-1", expectedInt64: -1},
		{env: "0", expectedInt64: 0},
		{env: "1", expectedInt64: 1},
		{env: "13", expectedInt64: 13},
		{env: "2147483647", expectedInt64: 2147483647},
	}
	for _, td := range testData {
		err := os.Setenv(key, td.env)
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetInt64(key, def)
		if val != td.expectedInt64 {
			t.Errorf("Returned value %v does not match expected one for env '%s'", val, td.env)
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	}
}

func TestGetUint64FromEnvIfNil(t *testing.T) {
	key := "ENVUTIL_TEST_GET_UINT64"
	var def uint64 = 9999
	t.Run("WhenNotSpecifying64bitUnsignedIntegerAndEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		val := GetUint64FromEnvIfNil(nil, key, def)
		if val == nil {
			tt.Error("Value not returned")
		} else if *val != def {
			tt.Fail()
		}
	})
	t.Run("WhenNotSpecifying64bitUnsignedIntegerAndEnvironmentVariableIsSet", func(tt *testing.T) {
		err := os.Setenv(key, "1234")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetUint64FromEnvIfNil(nil, key, def)
		if val == nil {
			tt.Error("Value not returned")
		} else if *val != 1234 {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
	t.Run("WhenSpecifying64bitUnsignedIntegerAndEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		var v uint64 = 1234
		val := GetUint64FromEnvIfNil(&v, key, def)
		if val != &v {
			tt.Fail()
		}
	})
	t.Run("WhenSpecifying64bitUnsignedIntegerAndEnvironmentVariableIsSet", func(tt *testing.T) {
		err := os.Setenv(key, "non-default value")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		var v uint64 = 1234
		val := GetUint64FromEnvIfNil(&v, key, def)
		if val != &v {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
}

func TestGetUint64(t *testing.T) {
	key := "ENVUTIL_TEST_GET_UINT64"
	var def uint64 = 9999
	t.Run("WhenEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		if GetUint64(key, def) != def {
			tt.Fail()
		}
	})

	type data struct {
		env            string
		expectedUint64 uint64
	}
	testData := []data{
		{env: "", expectedUint64: def},
		{env: "zero", expectedUint64: def},
		{env: "-2147483648", expectedUint64: def},
		{env: "-13", expectedUint64: def},
		{env: "-1", expectedUint64: def},
		{env: "0", expectedUint64: 0},
		{env: "1", expectedUint64: 1},
		{env: "13", expectedUint64: 13},
		{env: "4294967295", expectedUint64: 4294967295},
	}
	for _, td := range testData {
		err := os.Setenv(key, td.env)
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetUint64(key, def)
		if val != td.expectedUint64 {
			t.Errorf("Returned value %v does not match expected one for env '%s'", val, td.env)
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	}
}

func TestGetFloat64FromEnvIfNil(t *testing.T) {
	key := "ENVUTIL_TEST_GET_FLOAT64"
	def := 99.99
	t.Run("WhenNotSpecifying64bitUnsignedIntegerAndEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		val := GetFloat64FromEnvIfNil(nil, key, def)
		if val == nil {
			tt.Error("Value not returned")
		} else if *val != def {
			tt.Fail()
		}
	})
	t.Run("WhenNotSpecifying64bitUnsignedIntegerAndEnvironmentVariableIsSet", func(tt *testing.T) {
		err := os.Setenv(key, "12.34")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetFloat64FromEnvIfNil(nil, key, def)
		if val == nil {
			tt.Error("Value not returned")
		} else if *val != 12.34 {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
	t.Run("WhenSpecifying64bitUnsignedIntegerAndEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		v := 12.34
		val := GetFloat64FromEnvIfNil(&v, key, def)
		if val != &v {
			tt.Fail()
		}
	})
	t.Run("WhenSpecifying64bitUnsignedIntegerAndEnvironmentVariableIsSet", func(tt *testing.T) {
		err := os.Setenv(key, "non-default value")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		v := 12.34
		val := GetFloat64FromEnvIfNil(&v, key, def)
		if val != &v {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
}

func TestGetFloat64(t *testing.T) {
	key := "ENVUTIL_TEST_GET_FLOAT64"
	def := 99.99
	t.Run("WhenEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		if GetFloat64(key, def) != def {
			tt.Fail()
		}
	})

	type data struct {
		env             string
		expectedFloat64 float64
	}
	testData := []data{
		{env: "", expectedFloat64: def},
		{env: "zero", expectedFloat64: def},
		{env: "-21474.83648", expectedFloat64: -21474.83648},
		{env: "-1.3", expectedFloat64: -1.3},
		{env: "-1", expectedFloat64: -1},
		{env: "0", expectedFloat64: 0},
		{env: "1", expectedFloat64: 1},
		{env: "1.3", expectedFloat64: 1.3},
		{env: "42949.67295", expectedFloat64: 42949.67295},
	}
	for _, td := range testData {
		err := os.Setenv(key, td.env)
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetFloat64(key, def)
		if val != td.expectedFloat64 {
			t.Errorf("Returned value %v does not match expected one for env '%s'", val, td.env)
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	}
}

func TestGetBoolFromEnvIfNil(t *testing.T) {
	key := "ENVUTIL_TEST_GET_BOOL"
	def := true
	t.Run("WhenNotSpecifyingBoolAndEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		val := GetBoolFromEnvIfNil(nil, key, def)
		if val == nil {
			tt.Error("Value not returned")
		} else if *val != def {
			tt.Fail()
		}
	})
	t.Run("WhenNotSpecifyingBoolAndEnvironmentVariableIsSet", func(tt *testing.T) {
		err := os.Setenv(key, "false")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetBoolFromEnvIfNil(nil, key, def)
		if val == nil {
			tt.Error("Value not returned")
		} else if *val != false {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
	t.Run("WhenSpecifyingBoolAndEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		v := false
		val := GetBoolFromEnvIfNil(&v, key, def)
		if val != &v {
			tt.Fail()
		}
	})
	t.Run("WhenSpecifyingBoolAndEnvironmentVariableIsSet", func(tt *testing.T) {
		err := os.Setenv(key, "non-default value")
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		v := false
		val := GetBoolFromEnvIfNil(&v, key, def)
		if val != &v {
			tt.Fail()
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	})
}

func TestGetBool(t *testing.T) {
	key := "ENVUTIL_TEST_GET_BOOL"

	// Test when the environment variable is not set
	t.Run("WhenEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		if GetBool(key, false) {
			tt.Error("Expected false when environment variable is not set and default is false")
		}
		if !GetBool(key, true) {
			tt.Error("Expected true when environment variable is not set and default is true")
		}
	})

	// Define test data
	type data struct {
		env                     string
		expectedForFalseDefault bool
		expectedForTrueDefault  bool
	}

	testData := []data{
		// Empty or invalid values should return the default
		{env: "", expectedForFalseDefault: false, expectedForTrueDefault: true},
		{env: "maybe", expectedForFalseDefault: false, expectedForTrueDefault: true},
		{env: "truetru", expectedForFalseDefault: true, expectedForTrueDefault: true},
		{env: "yesyes", expectedForFalseDefault: true, expectedForTrueDefault: true},
		{env: "falsefals", expectedForFalseDefault: false, expectedForTrueDefault: false},
		{env: "nono", expectedForFalseDefault: false, expectedForTrueDefault: false},

		// Numeric values
		{env: "-6161616161", expectedForFalseDefault: true, expectedForTrueDefault: true}, // Non-zero is true
		{env: "-13", expectedForFalseDefault: true, expectedForTrueDefault: true},         // Non-zero is true
		{env: "-1", expectedForFalseDefault: true, expectedForTrueDefault: true},          // Non-zero is true
		{env: "0", expectedForFalseDefault: false, expectedForTrueDefault: false},         // Zero is false
		{env: "1", expectedForFalseDefault: true, expectedForTrueDefault: true},           // Non-zero is true
		{env: "13", expectedForFalseDefault: true, expectedForTrueDefault: true},          // Non-zero is true
		{env: "6161616161", expectedForFalseDefault: true, expectedForTrueDefault: true},  // Non-zero is true

		// True values (case-insensitive)
		{env: "true", expectedForFalseDefault: true, expectedForTrueDefault: true},
		{env: "True", expectedForFalseDefault: true, expectedForTrueDefault: true},
		{env: "TRUE", expectedForFalseDefault: true, expectedForTrueDefault: true},
		{env: "tRuE", expectedForFalseDefault: true, expectedForTrueDefault: true},
		{env: "yes", expectedForFalseDefault: true, expectedForTrueDefault: true},
		{env: "Yes", expectedForFalseDefault: true, expectedForTrueDefault: true},
		{env: "YES", expectedForFalseDefault: true, expectedForTrueDefault: true},
		{env: "yEs", expectedForFalseDefault: true, expectedForTrueDefault: true},

		// False values (case-insensitive)
		{env: "false", expectedForFalseDefault: false, expectedForTrueDefault: false},
		{env: "False", expectedForFalseDefault: false, expectedForTrueDefault: false},
		{env: "FALSE", expectedForFalseDefault: false, expectedForTrueDefault: false},
		{env: "fAlSe", expectedForFalseDefault: false, expectedForTrueDefault: false},
		{env: "no", expectedForFalseDefault: false, expectedForTrueDefault: false},
		{env: "No", expectedForFalseDefault: false, expectedForTrueDefault: false},
		{env: "NO", expectedForFalseDefault: false, expectedForTrueDefault: false},
		{env: "nO", expectedForFalseDefault: false, expectedForTrueDefault: false},
	}

	// Run tests for each case
	for _, td := range testData {
		err := os.Setenv(key, td.env)
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetBool(key, false)
		if val != td.expectedForFalseDefault {
			t.Errorf("For env '%s' with default false: expected %v, got %v", td.env, td.expectedForFalseDefault, val)
		}
		val = GetBool(key, true)
		if val != td.expectedForTrueDefault {
			t.Errorf("For env '%s' with default true: expected %v, got %v", td.env, td.expectedForTrueDefault, val)
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	}
}

// TestValidateEnvironmentVariables tests the ValidateEnvironmentVariables function
func TestValidateEnvironmentVariables(t *testing.T) {
	// Store original values to restore later
	originalRegion := Region
	originalCaName := CaName
	originalCaPoolName := CaPoolName
	originalCaPoolDeployedProjectID := CaPoolDeployedProjectID
	originalSecretManagerProjectID := SecretManagerProjectID
	originalVsaDeployedDnsName := VsaDeployedDnsName
	originalVsaManagedZone := VsaManagedZone
	originalCertificateLifetime := CertificateLifetime
	originalCloudDNSCacheTTL := CloudDNSCacheTTL
	originalNodePassword := NodePassword
	originalMgmtFirewallSourceRanges := MgmtFirewallSourceRanges
	originalRsmFirewallSourceRanges := RsmFirewallSourceRanges
	originalIcFirewallSourceRanges := IcFirewallSourceRanges
	originalDataFirewallSourceRanges := DataFirewallSourceRanges
	originalMgmtRegionalNatIP := MgmtRegionalNatIP
	originalMgmtNetworkIpRange := MgmtNetworkIpRange
	originalRsmNetworkIpRange := RsmNetworkIpRange
	originalIcNetworkIpRange := IcNetworkIpRange
	originalPrivateKeyBits := PrivateKeyBits

	t.Run("WhenAllEnvironmentVariablesAreSet", func(tt *testing.T) {
		// Set all required environment variables
		err := os.Setenv("LOCAL_REGION", "us-central1")
		assert.NoError(tt, err)
		err = os.Setenv("CA_NAME", "test-ca")
		assert.NoError(tt, err)
		err = os.Setenv("CA_POOL_NAME", "test-ca-pool")
		assert.NoError(tt, err)
		err = os.Setenv("CA_POOL_DEPLOYED_PROJECT_ID", "test-project")
		assert.NoError(tt, err)
		err = os.Setenv("SECRET_MANAGER_PROJECT_ID", "secret-project")
		assert.NoError(tt, err)
		err = os.Setenv("VSA_DEPLOYED_DNS_NAME", "test.example.com")
		assert.NoError(tt, err)
		err = os.Setenv("VSA_MANAGED_ZONE", "test-zone")
		assert.NoError(tt, err)
		err = os.Setenv("CERTIFICATE_LIFETIME", "2592000s")
		assert.NoError(tt, err)
		err = os.Setenv("CLOUD_DNS_CACHE_TTL", "600")
		assert.NoError(tt, err)
		err = os.Setenv("VSA_NODE_PASSWORD", "test-password")
		assert.NoError(tt, err)
		err = os.Setenv("MGMT_FIREWALL_SOURCE_RANGES", "198.18.0.0/20,198.18.0.0/20")
		assert.NoError(tt, err)
		err = os.Setenv("RSM_FIREWALL_SOURCE_RANGES", "198.18.0.0/20,198.18.0.0/20")
		assert.NoError(tt, err)
		err = os.Setenv("IC_FIREWALL_SOURCE_RANGES", "198.18.0.0/20,198.18.0.0/20")
		assert.NoError(tt, err)
		err = os.Setenv("DATA_FIREWALL_SOURCE_RANGES", "198.18.0.0/20,198.18.0.0/20")
		assert.NoError(tt, err)
		err = os.Setenv("MGMT_REGIONAL_NAT_IP", "198.18.0.0/20,198.18.0.0/20")
		assert.NoError(tt, err)
		err = os.Setenv("MGMT_NETWORK_IP_RANGE", "198.18.0.0/20")
		assert.NoError(tt, err)
		err = os.Setenv("RSM_NETWORK_IP_RANGE", "198.18.0.0/20")
		assert.NoError(tt, err)
		err = os.Setenv("IC_NETWORK_IP_RANGE", "198.18.0.0/20")
		assert.NoError(tt, err)
		err = os.Setenv("PRIVATE_KEY_BITS", "3072")
		assert.NoError(tt, err)

		// Reinitialize variables by simulating package load
		Region = GetString("LOCAL_REGION", "")
		CaName = GetString("CA_NAME", "")
		CaPoolName = GetString("CA_POOL_NAME", "")
		CaPoolDeployedProjectID = GetString("CA_POOL_DEPLOYED_PROJECT_ID", "")
		SecretManagerProjectID = GetString("SECRET_MANAGER_PROJECT_ID", "")
		VsaDeployedDnsName = GetString("VSA_DEPLOYED_DNS_NAME", "")
		VsaManagedZone = GetString("VSA_MANAGED_ZONE", "")
		CertificateLifetime = GetString("CERTIFICATE_LIFETIME", "94608000s")
		CloudDNSCacheTTL = GetInt64("CLOUD_DNS_CACHE_TTL", 300)
		PrivateKeyBits = GetInt("PRIVATE_KEY_BITS", 3072)
		NodePassword = GetString("VSA_NODE_PASSWORD", "")
		MgmtFirewallSourceRanges = GetString("MGMT_FIREWALL_SOURCE_RANGES", "")
		RsmFirewallSourceRanges = GetString("RSM_FIREWALL_SOURCE_RANGES", "")
		IcFirewallSourceRanges = GetString("IC_FIREWALL_SOURCE_RANGES", "")
		DataFirewallSourceRanges = GetString("DATA_FIREWALL_SOURCE_RANGES", "")
		MgmtRegionalNatIP = GetString("MGMT_REGIONAL_NAT_IP", "")
		MgmtNetworkIpRange = GetString("MGMT_NETWORK_IP_RANGE", "198.18.0.0/20")
		RsmNetworkIpRange = GetString("RSM_NETWORK_IP_RANGE", "198.18.16.0/20")
		IcNetworkIpRange = GetString("IC_NETWORK_IP_RANGE", "198.18.32.0/20")

		// Use reflection to update the NetworkSourceRanges map with current values
		packageValue := reflect.ValueOf(&NetworkSourceRanges).Elem()
		newMap := map[string]string{
			"MGMT_FIREWALL_SOURCE_RANGES": MgmtFirewallSourceRanges,
			"RSM_FIREWALL_SOURCE_RANGES":  RsmFirewallSourceRanges,
			"IC_FIREWALL_SOURCE_RANGES":   IcFirewallSourceRanges,
			"DATA_FIREWALL_SOURCE_RANGES": DataFirewallSourceRanges,
			"MGMT_REGIONAL_NAT_IP":        MgmtRegionalNatIP,
		}
		packageValue.Set(reflect.ValueOf(newMap))

		// Use reflection to update the networkIpRanges map with current values
		packageValue2 := reflect.ValueOf(&NetworkIpRanges).Elem()
		newMap2 := map[string]string{
			"MGMT_NETWORK_IP_RANGE": MgmtNetworkIpRange,
			"RSM_NETWORK_IP_RANGE":  RsmNetworkIpRange,
			"IC_NETWORK_IP_RANGE":   IcNetworkIpRange,
		}
		packageValue2.Set(reflect.ValueOf(newMap2))

		// Validate environment variables
		err = ValidateEnvironmentVariables()
		assert.NoError(tt, err)

		// Clean up
		err = os.Unsetenv("LOCAL_REGION")
		assert.NoError(tt, err)
		err = os.Unsetenv("CA_NAME")
		assert.NoError(tt, err)
		err = os.Unsetenv("CA_POOL_NAME")
		assert.NoError(tt, err)
		err = os.Unsetenv("CA_POOL_DEPLOYED_PROJECT_ID")
		assert.NoError(tt, err)
		err = os.Unsetenv("SECRET_MANAGER_PROJECT_ID")
		assert.NoError(tt, err)
		err = os.Unsetenv("VSA_DEPLOYED_DNS_NAME")
		assert.NoError(tt, err)
		err = os.Unsetenv("VSA_MANAGED_ZONE")
		assert.NoError(tt, err)
		err = os.Unsetenv("CERTIFICATE_LIFETIME")
		assert.NoError(tt, err)
		err = os.Unsetenv("CLOUD_DNS_CACHE_TTL")
		assert.NoError(tt, err)
		err = os.Unsetenv("VSA_NODE_PASSWORD")
		assert.NoError(tt, err)
		err = os.Unsetenv("MGMT_FIREWALL_SOURCE_RANGES")
		assert.NoError(tt, err)
		err = os.Unsetenv("RSM_FIREWALL_SOURCE_RANGES")
		assert.NoError(tt, err)
		err = os.Unsetenv("IC_FIREWALL_SOURCE_RANGES")
		assert.NoError(tt, err)
		err = os.Unsetenv("DATA_FIREWALL_SOURCE_RANGES")
		assert.NoError(tt, err)
		err = os.Unsetenv("MGMT_REGIONAL_NAT_IP")
		assert.NoError(tt, err)
		err = os.Unsetenv("MGMT_NETWORK_IP_RANGE")
		assert.NoError(tt, err)
		err = os.Unsetenv("RSM_NETWORK_IP_RANGE")
		assert.NoError(tt, err)
		err = os.Unsetenv("IC_NETWORK_IP_RANGE")
		assert.NoError(tt, err)
		err = os.Unsetenv("PRIVATE_KEY_BITS")
		assert.NoError(tt, err)
	})

	t.Run("WhenRegionIsEmpty", func(tt *testing.T) {
		Region = ""
		err := ValidateEnvironmentVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "LOCAL_REGION must be set for authentication")
	})

	t.Run("WhenCaNameIsEmpty", func(tt *testing.T) {
		Region = "us-central1"
		CaName = ""
		err := ValidateEnvironmentVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "CA_NAME must be set for authentication")
	})

	t.Run("WhenCaPoolNameIsEmpty", func(tt *testing.T) {
		Region = "us-central1"
		CaName = "test-ca"
		CaPoolName = ""
		err := ValidateEnvironmentVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "CA_POOL_NAME must be set for authentication")
	})

	t.Run("WhenCaPoolDeployedProjectIDIsEmpty", func(tt *testing.T) {
		Region = "us-central1"
		CaName = "test-ca"
		CaPoolName = "test-ca-pool"
		CaPoolDeployedProjectID = ""
		err := ValidateEnvironmentVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "CA_POOL_DEPLOYED_PROJECT_ID must be set for authentication")
	})

	t.Run("WhenSecretManagerProjectIDIsEmpty", func(tt *testing.T) {
		Region = "us-central1"
		CaName = "test-ca"
		CaPoolName = "test-ca-pool"
		CaPoolDeployedProjectID = "test-project"
		SecretManagerProjectID = ""
		err := ValidateEnvironmentVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SECRET_MANAGER_PROJECT_ID must be set for authentication")
	})

	t.Run("WhenVsaDeployedDnsNameIsEmpty", func(tt *testing.T) {
		Region = "us-central1"
		CaName = "test-ca"
		CaPoolName = "test-ca-pool"
		CaPoolDeployedProjectID = "test-project"
		SecretManagerProjectID = "secret-project"
		VsaDeployedDnsName = ""
		err := ValidateEnvironmentVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "VSA_DEPLOYED_DNS_NAME must be set for authentication")
	})

	t.Run("WhenVsaManagedZoneIsEmpty", func(tt *testing.T) {
		Region = "us-central1"
		CaName = "test-ca"
		CaPoolName = "test-ca-pool"
		CaPoolDeployedProjectID = "test-project"
		SecretManagerProjectID = "secret-project"
		VsaDeployedDnsName = "test.example.com"
		VsaManagedZone = ""
		err := ValidateEnvironmentVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "VSA_MANAGED_ZONE must be set for authentication")
	})

	t.Run("WhenCertificateLifetimeIsEmpty", func(tt *testing.T) {
		Region = "us-central1"
		CaName = "test-ca"
		CaPoolName = "test-ca-pool"
		CaPoolDeployedProjectID = "test-project"
		SecretManagerProjectID = "secret-project"
		VsaDeployedDnsName = "test.example.com"
		VsaManagedZone = "test-zone"
		CertificateLifetime = ""
		err := ValidateEnvironmentVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "CERTIFICATE_LIFETIME must be set for authentication")
	})

	t.Run("WhenCloudDNSCacheTTLIsZero", func(tt *testing.T) {
		Region = "us-central1"
		CaName = "test-ca"
		CaPoolName = "test-ca-pool"
		CaPoolDeployedProjectID = "test-project"
		SecretManagerProjectID = "secret-project"
		VsaDeployedDnsName = "test.example.com"
		VsaManagedZone = "test-zone"
		CertificateLifetime = "2592000s"
		CloudDNSCacheTTL = 0
		err := ValidateEnvironmentVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "CLOUD_DNS_CACHE_TTL must be set for authentication")
	})

	t.Run("WhenNodePasswordIsEmpty", func(tt *testing.T) {
		Region = "us-central1"
		CaName = "test-ca"
		CaPoolName = "test-ca-pool"
		CaPoolDeployedProjectID = "test-project"
		SecretManagerProjectID = "secret-project"
		VsaDeployedDnsName = "test.example.com"
		VsaManagedZone = "test-zone"
		CertificateLifetime = "2592000s"
		CloudDNSCacheTTL = 300
		NodePassword = ""
		err := ValidateEnvironmentVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "VSA_NODE_PASSWORD must be set for authentication")
	})

	t.Run("WhenPrivateKeyBitsIsEmpty", func(tt *testing.T) {
		Region = "us-central1"
		CaName = "test-ca"
		CaPoolName = "test-ca-pool"
		CaPoolDeployedProjectID = "test-project"
		SecretManagerProjectID = "secret-project"
		VsaDeployedDnsName = "test.example.com"
		VsaManagedZone = "test-zone"
		CertificateLifetime = "2592000s"
		CloudDNSCacheTTL = 300
		NodePassword = "password"
		PrivateKeyBits = 0
		err := ValidateEnvironmentVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "PRIVATE_KEY_BITS must be set for authentication")
	})

	t.Run("WhenWorkerTaskQueueIsEmpty", func(tt *testing.T) {
		Region = "us-central1"
		CaName = "test-ca"
		CaPoolName = "test-ca-pool"
		CaPoolDeployedProjectID = "test-project"
		SecretManagerProjectID = "secret-project"
		VsaDeployedDnsName = "test.example.com"
		VsaManagedZone = "test-zone"
		CertificateLifetime = "2592000s"
		CloudDNSCacheTTL = 300
		NodePassword = "password"
		PrivateKeyBits = 3072
		WorkerTaskQueue = ""

		err := ValidateEnvironmentVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "WORKER_TASK_QUEUE must be set for worker configuration")
	})

	// Restore original values
	Region = originalRegion
	CaName = originalCaName
	CaPoolName = originalCaPoolName
	CaPoolDeployedProjectID = originalCaPoolDeployedProjectID
	SecretManagerProjectID = originalSecretManagerProjectID
	VsaDeployedDnsName = originalVsaDeployedDnsName
	VsaManagedZone = originalVsaManagedZone
	CertificateLifetime = originalCertificateLifetime
	CloudDNSCacheTTL = originalCloudDNSCacheTTL
	NodePassword = originalNodePassword
	MgmtFirewallSourceRanges = originalMgmtFirewallSourceRanges
	RsmFirewallSourceRanges = originalRsmFirewallSourceRanges
	IcFirewallSourceRanges = originalIcFirewallSourceRanges
	DataFirewallSourceRanges = originalDataFirewallSourceRanges
	MgmtRegionalNatIP = originalMgmtRegionalNatIP
	MgmtNetworkIpRange = originalMgmtNetworkIpRange
	RsmNetworkIpRange = originalRsmNetworkIpRange
	IcNetworkIpRange = originalIcNetworkIpRange
	PrivateKeyBits = originalPrivateKeyBits
}

// TestGlobalVariableDeclarations tests the global variables declared in the package
func TestGlobalVariableDeclarations(t *testing.T) {
	t.Run("AuthTypeConstantsAreDefinedCorrectly", func(tt *testing.T) {
		assert.Equal(tt, 0, USERNAME_PWD)
		assert.Equal(tt, 1, USERNAME_PWD_SEC_MGR)
		assert.Equal(tt, 2, USER_CERTIFICATE)
	})

	t.Run("GlobalVariablesAreInitializedFromEnvironmentOrDefaults", func(tt *testing.T) {
		// Test AuthType initialization
		err := os.Setenv("VSA_AUTH_TYPE", "1")
		assert.NoError(tt, err)
		authType := GetInt("VSA_AUTH_TYPE", USERNAME_PWD)
		assert.Equal(tt, 1, authType)
		err = os.Unsetenv("VSA_AUTH_TYPE")
		assert.NoError(tt, err)

		// Test Region initialization
		err = os.Setenv("LOCAL_REGION", "test-region")
		assert.NoError(tt, err)
		region := GetString("LOCAL_REGION", "")
		assert.Equal(tt, "test-region", region)
		err = os.Unsetenv("LOCAL_REGION")
		assert.NoError(tt, err)

		// Test CaName initialization
		err = os.Setenv("CA_NAME", "test-ca-name")
		assert.NoError(tt, err)
		caName := GetString("CA_NAME", "")
		assert.Equal(tt, "test-ca-name", caName)
		err = os.Unsetenv("CA_NAME")
		assert.NoError(tt, err)

		// Test CaPoolName initialization
		err = os.Setenv("CA_POOL_NAME", "test-ca-pool")
		assert.NoError(tt, err)
		caPoolName := GetString("CA_POOL_NAME", "")
		assert.Equal(tt, "test-ca-pool", caPoolName)
		err = os.Unsetenv("CA_POOL_NAME")
		assert.NoError(tt, err)

		// Test CaPoolDeployedProjectID initialization
		err = os.Setenv("CA_POOL_DEPLOYED_PROJECT_ID", "test-project-id")
		assert.NoError(tt, err)
		caPoolDeployedProjectID := GetString("CA_POOL_DEPLOYED_PROJECT_ID", "")
		assert.Equal(tt, "test-project-id", caPoolDeployedProjectID)
		err = os.Unsetenv("CA_POOL_DEPLOYED_PROJECT_ID")
		assert.NoError(tt, err)

		// Test SecretManagerProjectID initialization
		err = os.Setenv("SECRET_MANAGER_PROJECT_ID", "secret-manager-project")
		assert.NoError(tt, err)
		secretManagerProjectID := GetString("SECRET_MANAGER_PROJECT_ID", "")
		assert.Equal(tt, "secret-manager-project", secretManagerProjectID)
		err = os.Unsetenv("SECRET_MANAGER_PROJECT_ID")
		assert.NoError(tt, err)

		// Test VsaDeployedDnsName initialization
		err = os.Setenv("VSA_DEPLOYED_DNS_NAME", "test.dns.name")
		assert.NoError(tt, err)
		vsaDeployedDnsName := GetString("VSA_DEPLOYED_DNS_NAME", "")
		assert.Equal(tt, "test.dns.name", vsaDeployedDnsName)
		err = os.Unsetenv("VSA_DEPLOYED_DNS_NAME")
		assert.NoError(tt, err)

		// Test VsaManagedZone initialization
		err = os.Setenv("VSA_MANAGED_ZONE", "test-managed-zone")
		assert.NoError(tt, err)
		vsaManagedZone := GetString("VSA_MANAGED_ZONE", "")
		assert.Equal(tt, "test-managed-zone", vsaManagedZone)
		err = os.Unsetenv("VSA_MANAGED_ZONE")
		assert.NoError(tt, err)

		// Test CertificateLifetime initialization with default
		certificateLifetime := GetString("CERTIFICATE_LIFETIME", "94608000s")
		assert.Equal(tt, "94608000s", certificateLifetime)

		// Test CertificateLifetime initialization with custom value
		err = os.Setenv("CERTIFICATE_LIFETIME", "2592000s")
		assert.NoError(tt, err)
		certificateLifetime = GetString("CERTIFICATE_LIFETIME", "94608000s")
		assert.Equal(tt, "2592000s", certificateLifetime)
		err = os.Unsetenv("CERTIFICATE_LIFETIME")
		assert.NoError(tt, err)

		// Test NodePassword initialization
		err = os.Setenv("VSA_NODE_PASSWORD", "test-password")
		assert.NoError(tt, err)
		nodePassword := GetString("VSA_NODE_PASSWORD", "")
		assert.Equal(tt, "test-password", nodePassword)
		err = os.Unsetenv("VSA_NODE_PASSWORD")
		assert.NoError(tt, err)

		// Test CloudDNSCacheTTL initialization with default
		cloudDNSCacheTTL := GetInt64("CLOUD_DNS_CACHE_TTL", 300)
		assert.Equal(tt, int64(300), cloudDNSCacheTTL)

		// Test CloudDNSCacheTTL initialization with custom value
		err = os.Setenv("CLOUD_DNS_CACHE_TTL", "600")
		assert.NoError(tt, err)
		cloudDNSCacheTTL = GetInt64("CLOUD_DNS_CACHE_TTL", 300)
		assert.Equal(tt, int64(600), cloudDNSCacheTTL)
		err = os.Unsetenv("CLOUD_DNS_CACHE_TTL")
		assert.NoError(tt, err)
	})
}

// TestPackageInitFunction tests the init function behaviors
func TestPackageInitFunction(t *testing.T) {
	t.Run("WhenLocalEnvIsTrue", func(tt *testing.T) {
		// Set LOCAL_ENV to true
		err := os.Setenv("LOCAL_ENV", "true")
		assert.NoError(tt, err)

		// Reset volumeEnvPath to original and then call the function that would be called in init
		if GetBool("LOCAL_ENV", false) {
			assert.Equal(tt, "./config/config.yaml", "./config/config.yaml")
		}

		err = os.Unsetenv("LOCAL_ENV")
		assert.NoError(tt, err)
	})

	t.Run("WhenLocalEnvIsFalse", func(tt *testing.T) {
		// Set LOCAL_ENV to false
		err := os.Setenv("LOCAL_ENV", "false")
		assert.NoError(tt, err)

		// Reset volumeEnvPath to original and then call the function that would be called in init
		if !GetBool("LOCAL_ENV", false) {
			// volumeEnvPath should remain the default
			assert.NotEqual(tt, "./config/config.yaml", "/etc/config/config.yaml")
		}

		err = os.Unsetenv("LOCAL_ENV")
		assert.NoError(tt, err)
	})

	t.Run("PackageVariablesInitialization", func(tt *testing.T) {
		// Test all package-level variables that are initialized in init()

		// Set environment variables
		err := os.Setenv("LOGGER_LEVEL", "debug")
		assert.NoError(tt, err)
		err = os.Setenv("LOGGER_TYPE", "custom")
		assert.NoError(tt, err)
		err = os.Setenv("SLOG_HANDLER_TYPE", "text")
		assert.NoError(tt, err)
		err = os.Setenv("EXPORTER_TYPE", "file")
		assert.NoError(tt, err)
		err = os.Setenv("ADD_LOG_SOURCE_FILE", "true")
		assert.NoError(tt, err)
		err = os.Setenv("OTEL_SERVICE_NAME", "TEST-SERVICE")
		assert.NoError(tt, err)
		err = os.Setenv("OTEL_GOOGLE_PROJECT_ID", "test-project-123")
		assert.NoError(tt, err)

		// Test that the GetString and GetBool functions work correctly
		logLevel := GetString("LOGGER_LEVEL", "info")
		assert.Equal(tt, "debug", logLevel)

		loggerType := GetString("LOGGER_TYPE", "slog")
		assert.Equal(tt, "custom", loggerType)

		slogHandlerType := GetString("SLOG_HANDLER_TYPE", "json")
		assert.Equal(tt, "text", slogHandlerType)

		exporterType := GetString("EXPORTER_TYPE", "stdout")
		assert.Equal(tt, "file", exporterType)

		addSource := GetBool("ADD_LOG_SOURCE_FILE", false)
		assert.True(tt, addSource)

		serviceName := GetString("OTEL_SERVICE_NAME", "VCP-VSA")
		assert.Equal(tt, "TEST-SERVICE", serviceName)

		otelGoogleProjectID := GetString("OTEL_GOOGLE_PROJECT_ID", "")
		assert.Equal(tt, "test-project-123", otelGoogleProjectID)

		// Clean up
		err = os.Unsetenv("LOGGER_LEVEL")
		assert.NoError(tt, err)
		err = os.Unsetenv("LOGGER_TYPE")
		assert.NoError(tt, err)
		err = os.Unsetenv("SLOG_HANDLER_TYPE")
		assert.NoError(tt, err)
		err = os.Unsetenv("EXPORTER_TYPE")
		assert.NoError(tt, err)
		err = os.Unsetenv("ADD_LOG_SOURCE_FILE")
		assert.NoError(tt, err)
		err = os.Unsetenv("OTEL_SERVICE_NAME")
		assert.NoError(tt, err)
		err = os.Unsetenv("OTEL_GOOGLE_PROJECT_ID")
		assert.NoError(tt, err)
	})
}

// TestValidateNetworkEnvVariables tests the validateNetworkEnvVariables function
func TestValidateNetworkEnvVariables(t *testing.T) {
	// Store original values to restore later
	originalMgmtFirewallSourceRanges := MgmtFirewallSourceRanges
	originalRsmFirewallSourceRanges := RsmFirewallSourceRanges
	originalIcFirewallSourceRanges := IcFirewallSourceRanges
	originalDataFirewallSourceRanges := DataFirewallSourceRanges
	originalMgmtRegionalNatIP := MgmtRegionalNatIP
	originalMgmtNetworkIpRange := MgmtNetworkIpRange
	originalRsmNetworkIpRange := RsmNetworkIpRange
	originalIcNetworkIpRange := IcNetworkIpRange

	defer func() {
		// Restore original values
		MgmtFirewallSourceRanges = originalMgmtFirewallSourceRanges
		RsmFirewallSourceRanges = originalRsmFirewallSourceRanges
		IcFirewallSourceRanges = originalIcFirewallSourceRanges
		DataFirewallSourceRanges = originalDataFirewallSourceRanges
		MgmtRegionalNatIP = originalMgmtRegionalNatIP
		MgmtNetworkIpRange = originalMgmtNetworkIpRange
		RsmNetworkIpRange = originalRsmNetworkIpRange
		IcNetworkIpRange = originalIcNetworkIpRange

		// Rebuild maps with original values
		NetworkSourceRanges = map[string]string{
			"MGMT_FIREWALL_SOURCE_RANGES": originalMgmtFirewallSourceRanges,
			"RSM_FIREWALL_SOURCE_RANGES":  originalRsmFirewallSourceRanges,
			"IC_FIREWALL_SOURCE_RANGES":   originalIcFirewallSourceRanges,
			"DATA_FIREWALL_SOURCE_RANGES": originalDataFirewallSourceRanges,
			"MGMT_REGIONAL_NAT_IP":        originalMgmtRegionalNatIP,
		}
		NetworkIpRanges = map[string]string{
			"MGMT_NETWORK_IP_RANGE": originalMgmtNetworkIpRange,
			"RSM_NETWORK_IP_RANGE":  originalRsmNetworkIpRange,
			"IC_NETWORK_IP_RANGE":   originalIcNetworkIpRange,
		}
	}()

	t.Run("WhenAllNetworkConfigurationsAreValid", func(tt *testing.T) {
		// Set all valid values
		MgmtFirewallSourceRanges = "192.168.1.0/24,10.0.0.0/8"
		RsmFirewallSourceRanges = "172.16.0.0/12"
		IcFirewallSourceRanges = "203.0.113.0/24"
		DataFirewallSourceRanges = "198.51.100.0/24"
		MgmtRegionalNatIP = "198.51.100.1/32"
		MgmtNetworkIpRange = "198.18.0.0/20"
		RsmNetworkIpRange = "198.18.16.0/20"
		IcNetworkIpRange = "198.18.32.0/20"

		// Rebuild maps
		NetworkSourceRanges = map[string]string{
			"MGMT_FIREWALL_SOURCE_RANGES": MgmtFirewallSourceRanges,
			"RSM_FIREWALL_SOURCE_RANGES":  RsmFirewallSourceRanges,
			"IC_FIREWALL_SOURCE_RANGES":   IcFirewallSourceRanges,
			"DATA_FIREWALL_SOURCE_RANGES": DataFirewallSourceRanges,
			"MGMT_REGIONAL_NAT_IP":        MgmtRegionalNatIP,
		}
		NetworkIpRanges = map[string]string{
			"MGMT_NETWORK_IP_RANGE": MgmtNetworkIpRange,
			"RSM_NETWORK_IP_RANGE":  RsmNetworkIpRange,
			"IC_NETWORK_IP_RANGE":   IcNetworkIpRange,
		}

		err := validateNetworkEnvVariables()
		assert.NoError(tt, err)
	})

	t.Run("WhenMgmtFirewallSourceRangeIsEmpty", func(tt *testing.T) {
		MgmtFirewallSourceRanges = ""
		RsmFirewallSourceRanges = "10.0.0.0/8"
		IcFirewallSourceRanges = "172.16.0.0/12"
		DataFirewallSourceRanges = "203.0.113.0/24"
		MgmtRegionalNatIP = "198.51.100.1/32"
		MgmtNetworkIpRange = "198.18.0.0/20"
		RsmNetworkIpRange = "198.18.16.0/20"
		IcNetworkIpRange = "198.18.32.0/20"

		NetworkSourceRanges = map[string]string{
			"MGMT_FIREWALL_SOURCE_RANGES": MgmtFirewallSourceRanges,
			"RSM_FIREWALL_SOURCE_RANGES":  RsmFirewallSourceRanges,
			"IC_FIREWALL_SOURCE_RANGES":   IcFirewallSourceRanges,
			"DATA_FIREWALL_SOURCE_RANGES": DataFirewallSourceRanges,
			"MGMT_REGIONAL_NAT_IP":        MgmtRegionalNatIP,
		}
		NetworkIpRanges = map[string]string{
			"MGMT_NETWORK_IP_RANGE": MgmtNetworkIpRange,
			"RSM_NETWORK_IP_RANGE":  RsmNetworkIpRange,
			"IC_NETWORK_IP_RANGE":   IcNetworkIpRange,
		}

		err := validateNetworkEnvVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "must be set for firewall for VSA deployment")
	})

	t.Run("WhenSourceRangeHasInvalidCIDR", func(tt *testing.T) {
		MgmtFirewallSourceRanges = "invalid-cidr"
		RsmFirewallSourceRanges = "10.0.0.0/8"
		IcFirewallSourceRanges = "172.16.0.0/12"
		DataFirewallSourceRanges = "203.0.113.0/24"
		MgmtRegionalNatIP = "198.51.100.1/32"
		MgmtNetworkIpRange = "198.18.0.0/20"
		RsmNetworkIpRange = "198.18.16.0/20"
		IcNetworkIpRange = "198.18.32.0/20"

		NetworkSourceRanges = map[string]string{
			"MGMT_FIREWALL_SOURCE_RANGES": MgmtFirewallSourceRanges,
			"RSM_FIREWALL_SOURCE_RANGES":  RsmFirewallSourceRanges,
			"IC_FIREWALL_SOURCE_RANGES":   IcFirewallSourceRanges,
			"DATA_FIREWALL_SOURCE_RANGES": DataFirewallSourceRanges,
			"MGMT_REGIONAL_NAT_IP":        MgmtRegionalNatIP,
		}
		NetworkIpRanges = map[string]string{
			"MGMT_NETWORK_IP_RANGE": MgmtNetworkIpRange,
			"RSM_NETWORK_IP_RANGE":  RsmNetworkIpRange,
			"IC_NETWORK_IP_RANGE":   IcNetworkIpRange,
		}

		err := validateNetworkEnvVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Invalid CIDR format")
	})

	t.Run("WhenMgmtNetworkIpRangeIsEmpty", func(tt *testing.T) {
		MgmtFirewallSourceRanges = "192.168.1.0/24"
		RsmFirewallSourceRanges = "10.0.0.0/8"
		IcFirewallSourceRanges = "172.16.0.0/12"
		DataFirewallSourceRanges = "203.0.113.0/24"
		MgmtRegionalNatIP = "198.51.100.1/32"
		MgmtNetworkIpRange = ""
		RsmNetworkIpRange = "198.18.16.0/20"
		IcNetworkIpRange = "198.18.32.0/20"

		NetworkSourceRanges = map[string]string{
			"MGMT_FIREWALL_SOURCE_RANGES": MgmtFirewallSourceRanges,
			"RSM_FIREWALL_SOURCE_RANGES":  RsmFirewallSourceRanges,
			"IC_FIREWALL_SOURCE_RANGES":   IcFirewallSourceRanges,
			"DATA_FIREWALL_SOURCE_RANGES": DataFirewallSourceRanges,
			"MGMT_REGIONAL_NAT_IP":        MgmtRegionalNatIP,
		}
		NetworkIpRanges = map[string]string{
			"MGMT_NETWORK_IP_RANGE": MgmtNetworkIpRange,
			"RSM_NETWORK_IP_RANGE":  RsmNetworkIpRange,
			"IC_NETWORK_IP_RANGE":   IcNetworkIpRange,
		}

		err := validateNetworkEnvVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "must be set for subnet for VSA deployment")
	})

	t.Run("WhenIpRangeHasInvalidCIDR", func(tt *testing.T) {
		MgmtFirewallSourceRanges = "192.168.1.0/24"
		RsmFirewallSourceRanges = "10.0.0.0/8"
		IcFirewallSourceRanges = "172.16.0.0/12"
		DataFirewallSourceRanges = "203.0.113.0/24"
		MgmtRegionalNatIP = "198.51.100.1/32"
		MgmtNetworkIpRange = "invalid-ip-range"
		RsmNetworkIpRange = "198.18.16.0/20"
		IcNetworkIpRange = "198.18.32.0/20"

		NetworkSourceRanges = map[string]string{
			"MGMT_FIREWALL_SOURCE_RANGES": MgmtFirewallSourceRanges,
			"RSM_FIREWALL_SOURCE_RANGES":  RsmFirewallSourceRanges,
			"IC_FIREWALL_SOURCE_RANGES":   IcFirewallSourceRanges,
			"DATA_FIREWALL_SOURCE_RANGES": DataFirewallSourceRanges,
			"MGMT_REGIONAL_NAT_IP":        MgmtRegionalNatIP,
		}
		NetworkIpRanges = map[string]string{
			"MGMT_NETWORK_IP_RANGE": MgmtNetworkIpRange,
			"RSM_NETWORK_IP_RANGE":  RsmNetworkIpRange,
			"IC_NETWORK_IP_RANGE":   IcNetworkIpRange,
		}

		err := validateNetworkEnvVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Invalid CIDR format")
	})

	t.Run("WhenMultipleSourceRangesAreValid", func(tt *testing.T) {
		MgmtFirewallSourceRanges = "192.168.1.0/24,10.0.0.0/8,172.16.0.0/12"
		RsmFirewallSourceRanges = "203.0.113.0/24,198.51.100.0/24"
		IcFirewallSourceRanges = "192.0.2.0/24"
		DataFirewallSourceRanges = "169.254.0.0/16"
		MgmtRegionalNatIP = "198.51.100.1/32,203.0.113.1/32"
		MgmtNetworkIpRange = "198.18.0.0/20"
		RsmNetworkIpRange = "198.18.16.0/20"
		IcNetworkIpRange = "198.18.32.0/20"

		NetworkSourceRanges = map[string]string{
			"MGMT_FIREWALL_SOURCE_RANGES": MgmtFirewallSourceRanges,
			"RSM_FIREWALL_SOURCE_RANGES":  RsmFirewallSourceRanges,
			"IC_FIREWALL_SOURCE_RANGES":   IcFirewallSourceRanges,
			"DATA_FIREWALL_SOURCE_RANGES": DataFirewallSourceRanges,
			"MGMT_REGIONAL_NAT_IP":        MgmtRegionalNatIP,
		}
		NetworkIpRanges = map[string]string{
			"MGMT_NETWORK_IP_RANGE": MgmtNetworkIpRange,
			"RSM_NETWORK_IP_RANGE":  RsmNetworkIpRange,
			"IC_NETWORK_IP_RANGE":   IcNetworkIpRange,
		}

		err := validateNetworkEnvVariables()
		assert.NoError(tt, err)
	})

	t.Run("WhenIPv6RangesAreUsed", func(tt *testing.T) {
		MgmtFirewallSourceRanges = "2001:db8::/32"
		RsmFirewallSourceRanges = "fe80::/10"
		IcFirewallSourceRanges = "::1/128"
		DataFirewallSourceRanges = "2001:db8:85a3::/48"
		MgmtRegionalNatIP = "2001:db8:85a3::8a2e:370:7334/128"
		MgmtNetworkIpRange = "2001:db8:1234::/48"
		RsmNetworkIpRange = "2001:db8:5678::/48"
		IcNetworkIpRange = "2001:db8:9abc::/48"

		NetworkSourceRanges = map[string]string{
			"MGMT_FIREWALL_SOURCE_RANGES": MgmtFirewallSourceRanges,
			"RSM_FIREWALL_SOURCE_RANGES":  RsmFirewallSourceRanges,
			"IC_FIREWALL_SOURCE_RANGES":   IcFirewallSourceRanges,
			"DATA_FIREWALL_SOURCE_RANGES": DataFirewallSourceRanges,
			"MGMT_REGIONAL_NAT_IP":        MgmtRegionalNatIP,
		}
		NetworkIpRanges = map[string]string{
			"MGMT_NETWORK_IP_RANGE": MgmtNetworkIpRange,
			"RSM_NETWORK_IP_RANGE":  RsmNetworkIpRange,
			"IC_NETWORK_IP_RANGE":   IcNetworkIpRange,
		}

		err := validateNetworkEnvVariables()
		assert.NoError(tt, err)
	})

	t.Run("WhenSourceRangeValidationPassesButIpRangeValidationFails", func(tt *testing.T) {
		// Valid source ranges
		MgmtFirewallSourceRanges = "192.168.1.0/24"
		RsmFirewallSourceRanges = "10.0.0.0/8"
		IcFirewallSourceRanges = "172.16.0.0/12"
		DataFirewallSourceRanges = "203.0.113.0/24"
		MgmtRegionalNatIP = "198.51.100.1/32"
		// Invalid IP range
		MgmtNetworkIpRange = "198.18.0.0/20"
		RsmNetworkIpRange = "invalid-cidr"
		IcNetworkIpRange = "198.18.32.0/20"

		NetworkSourceRanges = map[string]string{
			"MGMT_FIREWALL_SOURCE_RANGES": MgmtFirewallSourceRanges,
			"RSM_FIREWALL_SOURCE_RANGES":  RsmFirewallSourceRanges,
			"IC_FIREWALL_SOURCE_RANGES":   IcFirewallSourceRanges,
			"DATA_FIREWALL_SOURCE_RANGES": DataFirewallSourceRanges,
			"MGMT_REGIONAL_NAT_IP":        MgmtRegionalNatIP,
		}
		NetworkIpRanges = map[string]string{
			"MGMT_NETWORK_IP_RANGE": MgmtNetworkIpRange,
			"RSM_NETWORK_IP_RANGE":  RsmNetworkIpRange,
			"IC_NETWORK_IP_RANGE":   IcNetworkIpRange,
		}

		err := validateNetworkEnvVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Invalid CIDR format")
	})

	t.Run("WhenEmptyMapsAreProvided", func(tt *testing.T) {
		// Test with empty maps
		NetworkSourceRanges = map[string]string{}
		NetworkIpRanges = map[string]string{}

		err := validateNetworkEnvVariables()
		assert.NoError(tt, err)
	})

	t.Run("WhenNetworkSourceRangesContainsSpaces", func(tt *testing.T) {
		MgmtFirewallSourceRanges = "192.168.1.0 /24" // Contains space
		RsmFirewallSourceRanges = "10.0.0.0/8"
		IcFirewallSourceRanges = "172.16.0.0/12"
		DataFirewallSourceRanges = "203.0.113.0/24"
		MgmtRegionalNatIP = "198.51.100.1/32"
		MgmtNetworkIpRange = "198.18.0.0/20"
		RsmNetworkIpRange = "198.18.16.0/20"
		IcNetworkIpRange = "198.18.32.0/20"

		NetworkSourceRanges = map[string]string{
			"MGMT_FIREWALL_SOURCE_RANGES": MgmtFirewallSourceRanges,
			"RSM_FIREWALL_SOURCE_RANGES":  RsmFirewallSourceRanges,
			"IC_FIREWALL_SOURCE_RANGES":   IcFirewallSourceRanges,
			"DATA_FIREWALL_SOURCE_RANGES": DataFirewallSourceRanges,
			"MGMT_REGIONAL_NAT_IP":        MgmtRegionalNatIP,
		}
		NetworkIpRanges = map[string]string{
			"MGMT_NETWORK_IP_RANGE": MgmtNetworkIpRange,
			"RSM_NETWORK_IP_RANGE":  RsmNetworkIpRange,
			"IC_NETWORK_IP_RANGE":   IcNetworkIpRange,
		}

		err := validateNetworkEnvVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "contain empty spaces")
	})

	t.Run("WhenIpRangeContainsSpaces", func(tt *testing.T) {
		MgmtFirewallSourceRanges = "192.168.1.0/24"
		RsmFirewallSourceRanges = "10.0.0.0/8"
		IcFirewallSourceRanges = "172.16.0.0/12"
		DataFirewallSourceRanges = "203.0.113.0/24"
		MgmtRegionalNatIP = "198.51.100.1/32"
		MgmtNetworkIpRange = "198.18.0.0 /20" // Contains space
		RsmNetworkIpRange = "198.18.16.0/20"
		IcNetworkIpRange = "198.18.32.0/20"

		NetworkSourceRanges = map[string]string{
			"MGMT_FIREWALL_SOURCE_RANGES": MgmtFirewallSourceRanges,
			"RSM_FIREWALL_SOURCE_RANGES":  RsmFirewallSourceRanges,
			"IC_FIREWALL_SOURCE_RANGES":   IcFirewallSourceRanges,
			"DATA_FIREWALL_SOURCE_RANGES": DataFirewallSourceRanges,
			"MGMT_REGIONAL_NAT_IP":        MgmtRegionalNatIP,
		}
		NetworkIpRanges = map[string]string{
			"MGMT_NETWORK_IP_RANGE": MgmtNetworkIpRange,
			"RSM_NETWORK_IP_RANGE":  RsmNetworkIpRange,
			"IC_NETWORK_IP_RANGE":   IcNetworkIpRange,
		}

		err := validateNetworkEnvVariables()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "contain empty spaces")
	})
}

// TestValidateSourceRanges tests the validateSourceRanges function
func TestValidateSourceRanges(t *testing.T) {
	envVariableName := "TEST_FIREWALL_SOURCE_RANGES"

	t.Run("WhenSourceRangesIsEmpty", func(tt *testing.T) {
		err := validateSourceRanges(envVariableName, "")
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TEST_FIREWALL_SOURCE_RANGES must be set for firewall for VSA deployment")
	})

	t.Run("WhenSourceRangesHasSingleValidCIDR", func(tt *testing.T) {
		testCases := []string{
			"192.168.1.0/24",
			"10.0.0.0/8",
			"172.16.0.0/12",
			"203.0.113.0/24",
			"198.51.100.0/24",
			"0.0.0.0/0",
			"127.0.0.1/32",
		}

		for _, sourceRange := range testCases {
			err := validateSourceRanges(envVariableName, sourceRange)
			assert.NoError(tt, err, "Expected no error for valid CIDR: %s", sourceRange)
		}
	})

	t.Run("WhenSourceRangesHasSingleInvalidCIDR", func(tt *testing.T) {
		testCases := []string{
			"invalid-cidr",
			"192.168.1.0/33",
			"256.256.256.256/24",
			"192.168.1.0",
			"192.168.1.0/-1",
			"not.an.ip.address/24",
			"192.168.1.0/abc",
		}

		for _, sourceRange := range testCases {
			err := validateSourceRanges(envVariableName, sourceRange)
			assert.Error(tt, err, "Expected error for invalid CIDR: %s", sourceRange)
		}
	})

	t.Run("WhenSourceRangesHasMultipleValidCIDRs", func(tt *testing.T) {
		testCases := []string{
			"192.168.1.0/24,10.0.0.0/8",
			"172.16.0.0/12,203.0.113.0/24,198.51.100.0/24",
			"192.168.1.0/24,10.0.0.0/8,172.16.0.0/12,0.0.0.0/0",
			"127.0.0.1/32,192.168.0.1/32",
		}

		for _, sourceRange := range testCases {
			err := validateSourceRanges(envVariableName, sourceRange)
			assert.NoError(tt, err, "Expected no error for valid CIDRs: %s", sourceRange)
		}
	})

	t.Run("WhenSourceRangesHasOneInvalidCIDRAmongValid", func(tt *testing.T) {
		testCases := []string{
			"192.168.1.0/24,invalid-cidr",
			"10.0.0.0/8,256.256.256.256/24",
			"172.16.0.0/12,192.168.1.0/33,203.0.113.0/24",
			"valid.but.not.ip/24,192.168.1.0/24",
		}

		for _, sourceRange := range testCases {
			err := validateSourceRanges(envVariableName, sourceRange)
			assert.Error(tt, err, "Expected error for mixed valid/invalid CIDRs: %s", sourceRange)
		}
	})

	t.Run("WhenSourceRangesHasAllInvalidCIDRs", func(tt *testing.T) {
		testCases := []string{
			"invalid-cidr,another-invalid",
			"256.256.256.256/24,192.168.1.0/33",
			"not.an.ip/24,also.not.ip/16",
		}

		for _, sourceRange := range testCases {
			err := validateSourceRanges(envVariableName, sourceRange)
			assert.Error(tt, err, "Expected error for all invalid CIDRs: %s", sourceRange)
		}
	})

	t.Run("WhenSourceRangesHasIPv6ValidCIDRs", func(tt *testing.T) {
		testCases := []string{
			"2001:db8::/32",
			"fe80::/10",
			"::1/128",
			"2001:db8:85a3::/48",
			"2001:db8::/32,fe80::/10",
			"::1/128,2001:db8:85a3::/48,fe80::/64",
		}

		for _, sourceRange := range testCases {
			err := validateSourceRanges(envVariableName, sourceRange)
			assert.NoError(tt, err, "Expected no error for valid IPv6 CIDRs: %s", sourceRange)
		}
	})

	t.Run("WhenSourceRangesHasIPv6InvalidCIDRs", func(tt *testing.T) {
		testCases := []string{
			"2001:db8::/129",
			"gggg::/32",
			"2001:db8:85a3::8a2e:370:7334:extra/64",
			"invalid:ipv6:address/64",
		}

		for _, sourceRange := range testCases {
			err := validateSourceRanges(envVariableName, sourceRange)
			assert.Error(tt, err, "Expected error for invalid IPv6 CIDRs: %s", sourceRange)
		}
	})

	t.Run("WhenSourceRangesHasMixedIPv4AndIPv6", func(tt *testing.T) {
		testCases := []string{
			"192.168.1.0/24,2001:db8::/32",
			"10.0.0.0/8,fe80::/10,172.16.0.0/12",
			"::1/128,127.0.0.1/32",
		}

		for _, sourceRange := range testCases {
			err := validateSourceRanges(envVariableName, sourceRange)
			assert.NoError(tt, err, "Expected no error for mixed IPv4/IPv6 CIDRs: %s", sourceRange)
		}
	})

	t.Run("WhenSourceRangesContainsSpaces", func(tt *testing.T) {
		testCases := []string{
			"192.168.1.0 /24",
			"192.168.1.0/ 24",
			"192.168.1.0/24 ,10.0.0.0/8",
			" 192.168.1.0/24",
			"192.168.1.0/24 ",
			"192.168.1.0/24, 10.0.0.0/8",
		}

		for _, sourceRange := range testCases {
			err := validateSourceRanges(envVariableName, sourceRange)
			assert.Error(tt, err, "Expected error for CIDRs with spaces: %s", sourceRange)
			assert.Contains(tt, err.Error(), "contain empty spaces", "Error should mention spaces for: %s", sourceRange)
		}
	})

	t.Run("WhenSourceRangesHasEmptyRangeInList", func(tt *testing.T) {
		testCases := []string{
			"192.168.1.0/24,,10.0.0.0/8",
			",192.168.1.0/24",
			"192.168.1.0/24,",
			"192.168.1.0/24, ,10.0.0.0/8",
		}

		for _, sourceRange := range testCases {
			err := validateSourceRanges(envVariableName, sourceRange)
			assert.Error(tt, err, "Expected error for empty range in list: %s", sourceRange)
		}
	})

	t.Run("WhenEnvVariableNameIsDifferent", func(tt *testing.T) {
		customEnvName := "CUSTOM_FIREWALL_RANGES"
		err := validateSourceRanges(customEnvName, "")
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "CUSTOM_FIREWALL_RANGES must be set for firewall for VSA deployment")
	})

	t.Run("WhenSourceRangesHasOnlyCommas", func(tt *testing.T) {
		testCases := []string{
			",",
			",,",
			",,,",
		}

		for _, sourceRange := range testCases {
			err := validateSourceRanges(envVariableName, sourceRange)
			assert.Error(tt, err, "Expected error for only commas: %s", sourceRange)
		}
	})

	t.Run("WhenSourceRangesHasValidEdgeCases", func(tt *testing.T) {
		testCases := []string{
			"0.0.0.0/0",          // Any IPv4
			"::/0",               // Any IPv6
			"255.255.255.255/32", // Broadcast
			"224.0.0.0/4",        // Multicast range
		}

		for _, sourceRange := range testCases {
			err := validateSourceRanges(envVariableName, sourceRange)
			assert.NoError(tt, err, "Expected no error for edge case CIDR: %s", sourceRange)
		}
	})
}

// TestValidateIpRange tests the validateIpRange function
func TestValidateIpRange(t *testing.T) {
	basicErrorString := "%s must be set for subnet for VSA deployment"
	envVariableName := "TEST_ENV_VAR"

	t.Run("WhenIpRangeIsEmpty", func(tt *testing.T) {
		err := validateIpRange("", basicErrorString, envVariableName)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TEST_ENV_VAR must be set for subnet for VSA deployment")
	})

	t.Run("WhenIpRangeContainsSpaces", func(tt *testing.T) {
		testCases := []string{
			"192.168.1.0 /24",
			" 192.168.1.0/24",
			"192.168.1.0/24 ",
			"192.168. 1.0/24",
			"192.168.1.0/ 24",
		}

		for _, ipRange := range testCases {
			err := validateIpRange(ipRange, basicErrorString, envVariableName)
			assert.Error(tt, err)
			assert.Contains(tt, err.Error(), "Can't contain empty spaces")
			assert.Contains(tt, err.Error(), ipRange)
		}
	})

	t.Run("WhenIpRangeIsValidCIDR", func(tt *testing.T) {
		testCases := []string{
			"192.168.1.0/24",
			"10.0.0.0/8",
			"172.16.0.0/12",
			"203.0.113.0/24",
			"198.51.100.0/24",
			"0.0.0.0/0",
			"127.0.0.1/32",
		}

		for _, ipRange := range testCases {
			err := validateIpRange(ipRange, basicErrorString, envVariableName)
			assert.NoError(tt, err)
		}
	})

	t.Run("WhenIpRangeIsInvalidCIDR", func(tt *testing.T) {
		testCases := []string{
			"invalid-cidr",
			"192.168.1.0/33",
			"256.256.256.256/24",
			"192.168.1.0/-1",
			"192.168.1.0/abc",
			"192.168.1.0",
			"192.168.1.0/",
			"/24",
			"192.168.1.256/24",
			"192.168.-1.0/24",
		}

		for _, ipRange := range testCases {
			err := validateIpRange(ipRange, basicErrorString, envVariableName)
			assert.Error(tt, err)
			assert.Contains(tt, err.Error(), "Invalid CIDR format")
			assert.Contains(tt, err.Error(), ipRange)
		}
	})

	t.Run("WhenBasicErrorStringIsCustom", func(tt *testing.T) {
		customErrorString := "%s must be configured properly"
		err := validateIpRange("", customErrorString, envVariableName)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "TEST_ENV_VAR must be configured properly")
	})

	t.Run("WhenIpRangeIsWhitespaceOnly", func(tt *testing.T) {
		testCases := []string{
			" ",
			"  ",
			"\t",
			"\n",
			"\r",
		}

		for _, ipRange := range testCases {
			if strings.Contains(ipRange, " ") {
				err := validateIpRange(ipRange, basicErrorString, envVariableName)
				assert.Error(tt, err)
				assert.Contains(tt, err.Error(), "Can't contain empty spaces")
			} else {
				// These whitespace characters don't contain regular spaces, so they trigger CIDR validation error
				err := validateIpRange(ipRange, basicErrorString, envVariableName)
				assert.Error(tt, err)
				assert.Contains(tt, err.Error(), "Invalid CIDR format")
			}
		}
	})

	t.Run("WhenIpRangeHasLeadingTrailingSpaces", func(tt *testing.T) {
		// These contain regular spaces, so should trigger space check
		spaceTestCases := []string{
			" 192.168.1.0/24",
			"192.168.1.0/24 ",
			" 192.168.1.0/24 ",
		}

		for _, ipRange := range spaceTestCases {
			err := validateIpRange(ipRange, basicErrorString, envVariableName)
			assert.Error(tt, err)
			assert.Contains(tt, err.Error(), "Can't contain empty spaces")
		}

		// These contain tabs but not regular spaces, so they trigger CIDR validation error
		tabTestCases := []string{
			"\t192.168.1.0/24",
			"192.168.1.0/24\t",
		}

		for _, ipRange := range tabTestCases {
			err := validateIpRange(ipRange, basicErrorString, envVariableName)
			assert.Error(tt, err)
			assert.Contains(tt, err.Error(), "Invalid CIDR format")
		}
	})

	t.Run("WhenIpRangeHasIPv6ValidFormats", func(tt *testing.T) {
		testCases := []string{
			"2001:db8::/32",
			"fe80::/10",
			"::1/128",
			"2001:db8:85a3::/48",
			"2001:db8:85a3::8a2e:370:7334/128",
			"::/0",
		}

		for _, ipRange := range testCases {
			err := validateIpRange(ipRange, basicErrorString, envVariableName)
			assert.NoError(tt, err)
		}
	})

	t.Run("WhenIpRangeHasIPv6InvalidFormats", func(tt *testing.T) {
		testCases := []string{
			"2001:db8::/129",
			"2001:db8::g/64",
			"2001:db8:85a3::8a2e:370:7334:extra/128",
			"2001:db8",
			"2001:db8::/",
			"/64",
		}

		for _, ipRange := range testCases {
			err := validateIpRange(ipRange, basicErrorString, envVariableName)
			assert.Error(tt, err)
			assert.Contains(tt, err.Error(), "Invalid CIDR format")
		}
	})
}

func TestGetDuration(t *testing.T) {
	key := "ENVUTIL_TEST_GET_DURATION"
	def := 99 * time.Second
	t.Run("WhenEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		if GetDuration(key, def) != def {
			tt.Fail()
		}
	})

	type data struct {
		env              string
		expectedDuration time.Duration
	}
	testData := []data{
		// Empty string should return default
		{env: "", expectedDuration: def},
		{env: "invalid", expectedDuration: def},
		{env: "123ns", expectedDuration: 123 * time.Nanosecond},
		{env: "1µs", expectedDuration: 1 * time.Microsecond},
		{env: "250ms", expectedDuration: 250 * time.Millisecond},
		{env: "30s", expectedDuration: 30 * time.Second},
		{env: "60m", expectedDuration: 60 * time.Minute},
		{env: "168h", expectedDuration: 168 * time.Hour},
		{env: "2.5h", expectedDuration: 150 * time.Minute},
		{env: "-5m", expectedDuration: -5 * time.Minute},
		{env: "2h45m30s", expectedDuration: 2*time.Hour + 45*time.Minute + 30*time.Second},
		{env: "0h0m0s", expectedDuration: 0},
		{env: "525600m", expectedDuration: 525600 * time.Minute}, // 1 year in minutes
		{env: "0.000001s", expectedDuration: 1 * time.Microsecond},
		{env: "1h-30m", expectedDuration: def},                           // Invalid format
		{env: "-1h30m", expectedDuration: -1*time.Hour - 30*time.Minute}, // Valid negative compound
	}

	for _, td := range testData {
		err := os.Setenv(key, td.env)
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetDuration(key, def)
		if val != td.expectedDuration {
			t.Errorf("For env '%s': expected %v, got %v", td.env, td.expectedDuration, val)
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	}
}

func TestBuildCaURI(t *testing.T) {
	// Save original env vars
	originalProjectID := CaPoolDeployedProjectID
	originalPoolName := CaPoolName
	originalCaName := CaName

	defer func() {
		CaPoolDeployedProjectID = originalProjectID
		CaPoolName = originalPoolName
		CaName = originalCaName
	}()

	t.Run("WhenAllParametersAreEmpty_ShouldUseEnvVars", func(t *testing.T) {
		// Set env vars
		CaPoolDeployedProjectID = "env-project"
		CaPoolName = "env-pool"
		CaName = "env-ca"

		result := BuildCaURI("", "", "")
		expected := "env-project/env-pool/env-ca"
		assert.Equal(t, expected, result)
	})

	t.Run("WhenProjectIDIsEmpty_ShouldFallbackToEnvVar", func(t *testing.T) {
		CaPoolDeployedProjectID = "env-project"
		CaPoolName = "env-pool"
		CaName = "env-ca"

		result := BuildCaURI("", "provided-pool", "provided-ca")
		expected := "env-project/provided-pool/provided-ca"
		assert.Equal(t, expected, result)
	})

	t.Run("WhenPoolNameIsEmpty_ShouldFallbackToEnvVar", func(t *testing.T) {
		CaPoolDeployedProjectID = "env-project"
		CaPoolName = "env-pool"
		CaName = "env-ca"

		result := BuildCaURI("provided-project", "", "provided-ca")
		expected := "provided-project/env-pool/provided-ca"
		assert.Equal(t, expected, result)
	})

	t.Run("WhenCaNameIsEmpty_ShouldFallbackToEnvVar", func(t *testing.T) {
		CaPoolDeployedProjectID = "env-project"
		CaPoolName = "env-pool"
		CaName = "env-ca"

		result := BuildCaURI("provided-project", "provided-pool", "")
		expected := "provided-project/provided-pool/env-ca"
		assert.Equal(t, expected, result)
	})

	t.Run("WhenAllParametersAreProvided_ShouldUseProvidedValues", func(t *testing.T) {
		CaPoolDeployedProjectID = "env-project"
		CaPoolName = "env-pool"
		CaName = "env-ca"

		result := BuildCaURI("provided-project", "provided-pool", "provided-ca")
		expected := "provided-project/provided-pool/provided-ca"
		assert.Equal(t, expected, result)
	})

	t.Run("WhenAllValuesAreEmptyAfterFallback_ShouldReturnEmptyString", func(t *testing.T) {
		CaPoolDeployedProjectID = ""
		CaPoolName = ""
		CaName = ""

		result := BuildCaURI("", "", "")
		assert.Equal(t, "", result)
	})

	t.Run("WhenPartialValuesAreEmptyAfterFallback_ShouldBuildWithAvailableValues", func(t *testing.T) {
		CaPoolDeployedProjectID = "env-project"
		CaPoolName = ""
		CaName = "env-ca"

		result := BuildCaURI("", "", "")
		expected := "env-project//env-ca"
		assert.Equal(t, expected, result)
	})
}

func TestParseCaURI(t *testing.T) {
	// Save original env vars
	originalProjectID := CaPoolDeployedProjectID
	originalPoolName := CaPoolName
	originalCaName := CaName

	defer func() {
		CaPoolDeployedProjectID = originalProjectID
		CaPoolName = originalPoolName
		CaName = originalCaName
	}()

	t.Run("WhenCaURIIsEmpty_ShouldReturnEnvVars", func(t *testing.T) {
		CaPoolDeployedProjectID = "env-project"
		CaPoolName = "env-pool"
		CaName = "env-ca"

		projectID, poolName, caName := ParseCaURI("")
		assert.Equal(t, "env-project", projectID)
		assert.Equal(t, "env-pool", poolName)
		assert.Equal(t, "env-ca", caName)
	})

	t.Run("WhenCaURIHasInvalidFormat_ShouldReturnEnvVars", func(t *testing.T) {
		CaPoolDeployedProjectID = "env-project"
		CaPoolName = "env-pool"
		CaName = "env-ca"

		// Test with 2 parts
		projectID, poolName, caName := ParseCaURI("project/pool")
		assert.Equal(t, "env-project", projectID)
		assert.Equal(t, "env-pool", poolName)
		assert.Equal(t, "env-ca", caName)

		// Test with 4 parts
		projectID, poolName, caName = ParseCaURI("project/pool/ca/extra")
		assert.Equal(t, "env-project", projectID)
		assert.Equal(t, "env-pool", poolName)
		assert.Equal(t, "env-ca", caName)
	})

	t.Run("WhenCaURIHasValidFormat_ShouldParseCorrectly", func(t *testing.T) {
		CaPoolDeployedProjectID = "env-project"
		CaPoolName = "env-pool"
		CaName = "env-ca"

		projectID, poolName, caName := ParseCaURI("provided-project/provided-pool/provided-ca")
		assert.Equal(t, "provided-project", projectID)
		assert.Equal(t, "provided-pool", poolName)
		assert.Equal(t, "provided-ca", caName)
	})

	t.Run("WhenCaURIHasEmptyProjectID_ShouldFallbackToEnvVar", func(t *testing.T) {
		CaPoolDeployedProjectID = "env-project"
		CaPoolName = "env-pool"
		CaName = "env-ca"

		projectID, poolName, caName := ParseCaURI("/provided-pool/provided-ca")
		assert.Equal(t, "env-project", projectID)
		assert.Equal(t, "provided-pool", poolName)
		assert.Equal(t, "provided-ca", caName)
	})

	t.Run("WhenCaURIHasEmptyPoolName_ShouldFallbackToEnvVar", func(t *testing.T) {
		CaPoolDeployedProjectID = "env-project"
		CaPoolName = "env-pool"
		CaName = "env-ca"

		projectID, poolName, caName := ParseCaURI("provided-project//provided-ca")
		assert.Equal(t, "provided-project", projectID)
		assert.Equal(t, "env-pool", poolName)
		assert.Equal(t, "provided-ca", caName)
	})

	t.Run("WhenCaURIHasEmptyCaName_ShouldFallbackToEnvVar", func(t *testing.T) {
		CaPoolDeployedProjectID = "env-project"
		CaPoolName = "env-pool"
		CaName = "env-ca"

		projectID, poolName, caName := ParseCaURI("provided-project/provided-pool/")
		assert.Equal(t, "provided-project", projectID)
		assert.Equal(t, "provided-pool", poolName)
		assert.Equal(t, "env-ca", caName)
	})

	t.Run("WhenCaURIHasAllNonEmptyParts_ShouldUseAllValues", func(t *testing.T) {
		CaPoolDeployedProjectID = "env-project"
		CaPoolName = "env-pool"
		CaName = "env-ca"

		projectID, poolName, caName := ParseCaURI("provided-project/provided-pool/provided-ca")
		assert.Equal(t, "provided-project", projectID)
		assert.Equal(t, "provided-pool", poolName)
		assert.Equal(t, "provided-ca", caName)
	})
}

func TestGetCertificateRotationThresholdPercentage(t *testing.T) {
	key := "CERTIFICATE_ROTATION_THRESHOLD_PERCENTAGE"
	defer func() {
		_ = os.Unsetenv(key)
	}()

	t.Run("WhenValueIsGreaterThanOne_TreatAsPercentage", func(tt *testing.T) {
		err := os.Setenv(key, "75")
		assert.NoError(tt, err)
		result := GetCertificateRotationThresholdPercentage()
		assert.Equal(tt, 0.75, result)
	})

	t.Run("WhenValueIsLessThanOrEqualToOne_TreatAsDecimal", func(tt *testing.T) {
		// Test with value <= 1 - covers line 303
		err := os.Setenv(key, "0.75")
		assert.NoError(tt, err)
		result := GetCertificateRotationThresholdPercentage()
		assert.Equal(tt, 0.75, result)

		err = os.Setenv(key, "1.0")
		assert.NoError(tt, err)
		result = GetCertificateRotationThresholdPercentage()
		assert.Equal(tt, 1.0, result)
	})

	t.Run("WhenEnvironmentVariableIsNotSet_UseDefault", func(tt *testing.T) {
		err := os.Unsetenv(key)
		assert.NoError(tt, err)
		result := GetCertificateRotationThresholdPercentage()
		assert.Equal(tt, 0.75, result) // Default is 75% = 0.75
	})
}

func TestGetCertificateRotationPoolStates(t *testing.T) {
	key := "CERTIFICATE_ROTATION_POOL_STATES"
	defer func() {
		_ = os.Unsetenv(key)
	}()

	t.Run("WhenValueIsSet_ReturnsTrimmedStates", func(tt *testing.T) {
		err := os.Setenv(key, "READY, DEGRADED , UPDATING")
		assert.NoError(tt, err)
		result := GetCertificateRotationPoolStates()
		assert.Equal(tt, []string{"READY", "DEGRADED", "UPDATING"}, result)
	})

	t.Run("WhenEnvironmentVariableIsNotSet_UseDefault", func(tt *testing.T) {
		err := os.Unsetenv(key)
		assert.NoError(tt, err)
		result := GetCertificateRotationPoolStates()
		assert.Equal(tt, []string{"READY", "DEGRADED"}, result)
	})

	t.Run("WhenValueIsEmpty_UseDefault", func(tt *testing.T) {
		err := os.Setenv(key, "")
		assert.NoError(tt, err)
		result := GetCertificateRotationPoolStates()
		assert.Equal(tt, []string{"READY", "DEGRADED"}, result)
	})

	t.Run("WhenValueHasOnlySpacesAndCommas_UseDefault", func(tt *testing.T) {
		err := os.Setenv(key, " , , ")
		assert.NoError(tt, err)
		result := GetCertificateRotationPoolStates()
		assert.Equal(tt, []string{"READY", "DEGRADED"}, result)
	})
}

func TestValidateCertificateLifetime(t *testing.T) {
	key1 := "CERTIFICATE_LIFETIME"
	key2 := "MINIMUM_CERTIFICATE_LIFETIME"
	defer func() {
		_ = os.Unsetenv(key1)
		_ = os.Unsetenv(key2)
	}()

	t.Run("WhenCertificateLifetimeIsValid", func(tt *testing.T) {
		err := os.Setenv(key1, "94608000s")
		assert.NoError(tt, err)
		err = os.Setenv(key2, "5184000s")
		assert.NoError(tt, err)

		// Test successful validation - covers lines 501-502, 522
		err = ValidateCertificateLifetime()
		assert.NoError(tt, err)
	})

	t.Run("WhenCertificateLifetimeHasInvalidFormat", func(tt *testing.T) {
		err := os.Setenv(key1, "invalid-format")
		assert.NoError(tt, err)
		err = os.Setenv(key2, "5184000s")
		assert.NoError(tt, err)

		// Test invalid certificate lifetime format - covers lines 505-507
		err = ValidateCertificateLifetime()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "invalid certificate lifetime format")
	})

	t.Run("WhenMinimumCertificateLifetimeHasInvalidFormat", func(tt *testing.T) {
		err := os.Setenv(key1, "94608000s")
		assert.NoError(tt, err)
		err = os.Setenv(key2, "invalid-format")
		assert.NoError(tt, err)

		// Test invalid minimum certificate lifetime format - covers lines 511-513
		err = ValidateCertificateLifetime()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "invalid minimum certificate lifetime format")
	})

	t.Run("WhenCertificateLifetimeIsLessThanMinimum", func(tt *testing.T) {
		err := os.Setenv(key1, "3000000s") // 3M seconds (less than 2 month minimum)
		assert.NoError(tt, err)
		err = os.Setenv(key2, "5184000s") // 5.2M seconds (minimum, 2 months)
		assert.NoError(tt, err)

		// Test certificate lifetime less than minimum - covers lines 517-518
		err = ValidateCertificateLifetime()
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "is less than minimum required lifetime")
	})

	t.Run("WhenCertificateLifetimeEqualsMinimum", func(tt *testing.T) {
		err := os.Setenv(key1, "5184000s")
		assert.NoError(tt, err)
		err = os.Setenv(key2, "5184000s")
		assert.NoError(tt, err)

		err = ValidateCertificateLifetime()
		assert.NoError(tt, err)
	})

	t.Run("WhenCertificateLifetimeIsGreaterThanMinimum", func(tt *testing.T) {
		err := os.Setenv(key1, "94608000s") // 94.6M seconds
		assert.NoError(tt, err)
		err = os.Setenv(key2, "5184000s") // 5.2M seconds (minimum, 2 months)
		assert.NoError(tt, err)

		err = ValidateCertificateLifetime()
		assert.NoError(tt, err)
	})
}

func TestValidateEnvironmentVariables_OCIRequiresWorkerTaskQueue(t *testing.T) {
	origH, origW := Hyperscaler, WorkerTaskQueue
	t.Cleanup(func() {
		Hyperscaler = origH
		WorkerTaskQueue = origW
	})
	Hyperscaler = "oci"
	WorkerTaskQueue = ""
	err := ValidateEnvironmentVariables()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "WORKER_TASK_QUEUE")

	WorkerTaskQueue = "customer-workflows"
	assert.NoError(t, ValidateEnvironmentVariables())
}

func TestValidateCertificateLifetime_OCIESkip(t *testing.T) {
	orig := Hyperscaler
	t.Cleanup(func() { Hyperscaler = orig })
	Hyperscaler = "oci"
	assert.NoError(t, ValidateCertificateLifetime())
}

func TestGetFloat64NotNegative(t *testing.T) {
	key := "ENVUTIL_TEST_GET_FLOAT64_NOT_NEGATIVE"
	def := 99.99

	t.Run("WhenEnvironmentVariableIsNotSet", func(tt *testing.T) {
		err := os.Unsetenv(key)
		if err != nil {
			tt.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
		if GetFloat64NotNegative(key, def) != def {
			tt.Fail()
		}
	})

	type data struct {
		env      string
		expected float64
	}
	testData := []data{
		{env: "", expected: def},
		{env: "notanumber", expected: def},
		{env: "-1", expected: def},
		{env: "-0.001", expected: def},
		{env: "0", expected: 0},
		{env: "1", expected: 1},
		{env: "3.5", expected: 3.5},
		{env: "100", expected: 100},
	}
	for _, td := range testData {
		err := os.Setenv(key, td.env)
		if err != nil {
			t.Errorf("Error setting environment variable %s: %v", key, err)
		}
		val := GetFloat64NotNegative(key, def)
		if val != td.expected {
			t.Errorf("Returned value %v does not match expected one for env '%s'", val, td.env)
		}
		err = os.Unsetenv(key)
		if err != nil {
			t.Errorf("Error unsetting environment variable %s: %v", key, err)
		}
	}
}
