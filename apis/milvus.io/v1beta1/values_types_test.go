package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValues_MarshalUnmarshalJSON(t *testing.T) {
	values := Values{
		Data: map[string]interface{}{
			"key": "value",
		},
	}

	expected := `{"key":"value"}`
	actual, err := values.MarshalJSON()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	assert.Equal(t, expected, string(actual))

	var values2 Values
	err = values2.UnmarshalJSON(actual)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	assert.Equal(t, values, values2)
}
func TestValues_DeepCopyInfo(t *testing.T) {
	values := &Values{
		Data: map[string]interface{}{
			"key": "value",
		},
	}
	values2 := &Values{}
	values.DeepCopyInto(values2)
	assert.NotSame(t, values, values2)
	assert.NotSame(t, &values.Data, &values2.Data)
	assert.Equal(t, values, values2)
}
