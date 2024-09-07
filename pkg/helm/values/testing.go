package values

// dummyValues is for the convenience of test
type dummyValues struct {
}

func (dummyValues) GetDefaultValues(dependencyName DependencyKind, chartVersion ChartVersion) map[string]interface{} {
	return map[string]interface{}{}
}
