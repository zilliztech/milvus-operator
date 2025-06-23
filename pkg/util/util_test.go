package util

import (
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/yaml"
)

func TestGetNumberValue(t *testing.T) {
	vals := map[string]interface{}{
		"a": map[string]interface{}{
			"b": int64(1),
			"c": float64(2.2),
		},
	}

	ret, exist := GetNumberValue(vals, "a", "b")
	assert.True(t, exist)
	assert.Equal(t, float64(1), ret)
	ret, exist = GetNumberValue(vals, "a", "c")
	assert.True(t, exist)
	assert.Equal(t, float64(2.2), ret)
}

func TestGetStringValue(t *testing.T) {
	vals := map[string]interface{}{
		"a": map[string]interface{}{
			"b": "1",
		},
	}
	ret, exist := GetStringValue(vals, "a", "b")
	assert.True(t, exist)
	assert.Equal(t, "1", ret)
	ret, exist = GetStringValue(vals, "a", "c")
	assert.False(t, exist)
	assert.Equal(t, "", ret)
}

func Test_GetBoolValue_SetValue_DeleteValue(t *testing.T) {
	origin := map[string]interface{}{}

	_, found := GetBoolValue(origin, "l1", "l2", "l3")
	assert.False(t, found)

	SetValue(origin, true, "l1", "l2", "l3")

	val, found := GetBoolValue(origin, "l1", "l2", "l3")
	assert.True(t, found)
	assert.True(t, val)

	DeleteValue(origin, "l1", "l2", "l3")
	_, found = GetBoolValue(origin, "l1", "l2", "l3")
	assert.False(t, found)

}

func TestSetStringSlice(t *testing.T) {
	origin := map[string]interface{}{}
	slice := []string{"v1", "v2"}
	SetStringSlice(origin, slice, "l1", "l2", "l3")
	values := origin["l1"].(map[string]interface{})["l2"].(map[string]interface{})["l3"].([]interface{})
	assert.Equal(t, slice[0], values[0].(string))
	assert.Equal(t, slice[1], values[1].(string))
}

var mergeTests = []struct {
	def       map[string]interface{}
	overrides map[string]interface{}
	expected  map[string]interface{}
}{
	{
		def:       map[string]interface{}{"foo": "bar"},
		overrides: map[string]interface{}{},
		expected:  map[string]interface{}{"foo": "bar"},
	},
	{
		def:       map[string]interface{}{"foo": "bar"},
		overrides: map[string]interface{}{"foo": "baz"},
		expected:  map[string]interface{}{"foo": "baz"},
	},
	{
		def:       map[string]interface{}{"foo": "bar"},
		overrides: map[string]interface{}{"foo": []string{"baz", "qux"}},
		expected:  map[string]interface{}{"foo": []string{"baz", "qux"}},
	},
	{
		def: map[string]interface{}{
			"foo": "bar",
			"bar": map[string]interface{}{"key": "val"},
		},
		overrides: map[string]interface{}{
			"foo": "baz",
			"bar": map[string]interface{}{"val": "key"},
		},
		expected: map[string]interface{}{
			"foo": "baz",
			"bar": map[string]interface{}{
				"key": "val",
				"val": "key",
			},
		},
	},
	{
		def: map[string]interface{}{"foo": "bar"},
		overrides: map[string]interface{}{
			"foo": map[string]interface{}{"foo2": "bar2"},
		},
		expected: map[string]interface{}{
			"foo": map[string]interface{}{"foo2": "bar2"},
		},
	},
	{
		def: map[string]interface{}{
			"foo": map[string]interface{}{"foo2": "bar2"},
		},
		overrides: map[string]interface{}{"foo": "bar"},
		expected:  map[string]interface{}{"foo": "bar"},
	},
	{
		def: map[string]interface{}{
			"etcd": map[string]interface{}{
				"endpoint": []string{"ip1"},
			},
		},
		overrides: map[string]interface{}{
			"etcd": map[string]interface{}{
				"endpoint": []string{"ip2", "ip3"},
			},
		},
		expected: map[string]interface{}{
			"etcd": map[string]interface{}{
				"endpoint": []string{"ip2", "ip3"},
			},
		},
	},
	{
		def: nil,
		overrides: map[string]interface{}{
			"foo": "bar",
		},
		expected: nil,
	},
	{
		def: map[string]interface{}{
			"foo": "bar",
		},
		overrides: nil,
		expected: map[string]interface{}{
			"foo": "bar",
		},
	},
}

func TestMergeValues(t *testing.T) {
	for i, test := range mergeTests {
		MergeValues(test.def, test.overrides)
		actual := test.def
		assert.Equal(t, test.expected, actual, "Test #%d", i)
	}
}

func TestGetHostPort(t *testing.T) {
	endPoint := "host:8080"
	host, port := GetHostPort(endPoint)
	assert.Equal(t, "host", host)
	assert.Equal(t, int32(8080), port)

	endPoint = "hostOnly"
	host, port = GetHostPort(endPoint)
	assert.Equal(t, "hostOnly", host)
	assert.Equal(t, int32(80), port)

	endPoint = "host:badPort"
	host, port = GetHostPort(endPoint)
	assert.Equal(t, "host", host)
	assert.Equal(t, int32(80), port)
}

func TestGetTemplatedValues(t *testing.T) {
	template := `
k1: v1
k2: {{ .k2 }}
`
	values := map[string]interface{}{
		"k2": "v2",
	}
	ret, err := GetTemplatedValues(template, values)
	assert.NoError(t, err)
	config := map[string]interface{}{}
	yaml.Unmarshal(ret, &config)
	log.Print(string(ret))
	assert.Equal(t, "v1", config["k1"])
	assert.Equal(t, "v2", config["k2"])
}

func TestGetTemplatedValues_FormatError(t *testing.T) {
	template := `
k1: v1
k2: {{ .k2
`
	values := map[string]interface{}{}
	_, err := GetTemplatedValues(template, values)
	assert.Error(t, err)
}

func TestJoinErrors(t *testing.T) {
	errs := []error{}
	assert.Empty(t, JoinErrors(errs))

	errs = []error{errors.New("e1")}
	assert.Equal(t, "e1", JoinErrors(errs))

	errs = []error{errors.New("e1"), errors.New("e2")}
	assert.Equal(t, "e1; e2", JoinErrors(errs))
}

func TestCheckSum(t *testing.T) {
	assert.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", CheckSum([]byte("hello")))
}

func TestHTTPGetBytes(t *testing.T) {
	var testCase int
	testserver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if testCase == 0 {
			// ok
			w.WriteHeader(http.StatusOK)
		}
		if testCase == 1 {
			// code err
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	// connect failed
	_, err := HTTPGetBytes("localhost:10000")
	assert.Error(t, err)

	testCase = 0
	_, err = HTTPGetBytes(testserver.URL)
	assert.NoError(t, err)
	testCase = 1
	_, err = HTTPGetBytes(testserver.URL)
	assert.Error(t, err)
}

func TestDeepCopyValues(t *testing.T) {
	t.Run("origin value not changed", func(t *testing.T) {
		v1 := map[string]interface{}{
			"1": map[string]interface{}{
				"1.1": "v1",
			},
		}
		v1Copy := DeepCopyValues(v1)
		v2 := v1["1"].(map[string]interface{})
		v2["1.1"] = "v2"
		assert.Equal(t, v1["1"].(map[string]interface{})["1.1"], "v2")
		assert.Equal(t, v1Copy["1"].(map[string]interface{})["1.1"], "v1")
	})

	t.Run("panic marshal failed", func(t *testing.T) {
		v1 := map[string]interface{}{
			"s1": mockMarshal{
				marshalFail: true,
			},
		}
		assert.Panics(t, func() { DeepCopyValues(v1) })
	})

	t.Run("panic unmarshal failed", func(t *testing.T) {
		v1 := map[string]interface{}{
			"s1": mockMarshal{},
		}
		assert.Panics(t, func() { DeepCopyValues(v1) })
	})
}

type mockMarshal struct {
	marshalFail bool
}

func (v mockMarshal) MarshalJSON() ([]byte, error) {
	if v.marshalFail {
		return nil, errors.New("")
	}
	return []byte(""), nil
}

func (v mockMarshal) UnmarshalJSON(data []byte) error {
	return errors.New("")
}

func TestTruePtr(t *testing.T) {
	assert.True(t, *BoolPtr(true))
	assert.False(t, *BoolPtr(false))
}

func Test_DoWithBackoff(t *testing.T) {
	var err error
	err = DoWithBackoff("test", func() error {
		return nil
	}, 3, time.Second)
	assert.NoError(t, err)

	err = DoWithBackoff("test", func() error {
		return errors.New("test error")
	}, 3, time.Second)
	assert.Error(t, err)

	t.Run("failed then success", func(t *testing.T) {
		var i int
		err = DoWithBackoff("test", func() error {
			i++
			if i == 3 {
				return nil
			}
			return errors.New("test error")
		}, 3, time.Second)
		assert.NoError(t, err)
		assert.Equal(t, 3, i)
	})
}
