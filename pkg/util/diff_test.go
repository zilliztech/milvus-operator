package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiffStr(t *testing.T) {
	ret := DiffStr("str1", "str2")
	assert.NotEmpty(t, ret)
}
