package util

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/Masterminds/sprig"
	"github.com/coreos/go-semver/semver"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const MqTypeConfigKey = "messageQueue"

func GetSemanticVersion(version string) (*semver.Version, error) {
	return semver.NewVersion(strings.TrimPrefix(version, "v"))
}

// GetNumberValue supports int64 / float64 in values return as float64
// see https://datatracker.ietf.org/doc/html/rfc8259#section-6
func GetNumberValue(values map[string]interface{}, fields ...string) (float64, bool) {
	val, found, err := unstructured.NestedInt64(values, fields...)
	if err == nil && found {
		return float64(val), true
	}

	fval, found, err := unstructured.NestedFloat64(values, fields...)
	if err == nil && found {
		return fval, true
	}
	return 0, false
}

func GetBoolValue(values map[string]interface{}, fields ...string) (bool, bool) {
	val, found, err := unstructured.NestedBool(values, fields...)
	if err != nil || !found {
		return false, false
	}

	return val, true
}

func GetStringValue(values map[string]interface{}, fields ...string) (string, bool) {
	val, found, err := unstructured.NestedString(values, fields...)
	if err != nil || !found {
		return "", false
	}

	return val, true
}

func DeleteValue(values map[string]interface{}, fields ...string) {
	unstructured.RemoveNestedField(values, fields...)
}

// only contains types bool, int64, float64, string, []interface{}, map[string]interface{}, json.Number and nil
func SetValue(values map[string]interface{}, v interface{}, fields ...string) {
	unstructured.SetNestedField(values, v, fields...)
}

func SetStringSlice(values map[string]interface{}, v []string, fields ...string) {
	unstructured.SetNestedStringSlice(values, v, fields...)
}

// MergeValues merges patch into origin, you have to make sure origin is not nil, otherwise it won't work
func MergeValues(origin, patch map[string]interface{}) {
	if origin == nil || patch == nil {
		return
	}
	for patchK, patchV := range patch {
		if _, exist := origin[patchK]; !exist {
			origin[patchK] = patchV
			continue
		}

		originValues, ok := origin[patchK].(map[string]interface{})
		if !ok {
			origin[patchK] = patchV
			continue
		}

		patchValues, ok := patchV.(map[string]interface{})
		if !ok {
			origin[patchK] = patchV
			continue
		}

		MergeValues(originValues, patchValues)
	}
}

func GetHostPort(endpoint string) (string, int32) {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return endpoint, 80
	}

	portInt, err := strconv.Atoi(port)
	if err != nil {
		return host, 80
	}

	return host, int32(portInt)
}

func GetTemplatedValues(templateConfig string, values interface{}) ([]byte, error) {
	t, err := template.New("template").
		Funcs(sprig.TxtFuncMap()).Parse(templateConfig)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	err = t.Execute(&buf, values)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func JoinErrors(errs []error) string {
	es := make([]string, 0, len(errs))
	for _, e := range errs {
		es = append(es, e.Error())
	}
	return strings.Join(es, "; ")
}

func CheckSum(s []byte) string {
	h := sha256.New()
	h.Write(s)
	return fmt.Sprintf("%x", h.Sum(nil))
}

var DefaultHTTPTimeout = 15 * time.Second

func HTTPGetBytes(url string) ([]byte, error) {
	http.DefaultClient.Timeout = DefaultHTTPTimeout
	resp, err := http.Get(url)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get url")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("unexpected status code %d", resp.StatusCode)
	}
	ret, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read response body")
	}
	return ret, nil
}

func DeepCopyValues(input map[string]interface{}) map[string]interface{} {
	b, err := json.Marshal(input)
	if err != nil {
		panic(err)
	}

	var out map[string]interface{}

	err = json.Unmarshal(b, &out)
	if err != nil {
		panic(err)
	}

	return out
}

func BoolPtr(val bool) *bool {
	return &val
}

var DefaultBackOffInterval = time.Second * 1
var DefaultMaxRetry = 3

var logger logr.Logger = logr.Discard()

func SetLogger(l logr.Logger) {
	logger = l
}

func DoWithBackoff(name string, fn func() error, maxRetry int, backOff time.Duration) error {
	var err error
	for i := 0; i < maxRetry; i++ {
		err = fn()
		if err == nil {
			return nil
		}
		logger.Info("dowithbackoff failed", "func", name, "retry", i, "err", err)
		time.Sleep(backOff)
	}
	return errors.Wrapf(err, "dowithbackoff[%s] failed after %d retries", name, maxRetry)
}
