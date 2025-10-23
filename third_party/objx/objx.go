package objx

// Map is a minimal implementation used by testify/mock for storing arbitrary data.
type Map map[string]interface{}

// Set stores a value for the provided key and returns the map for chaining.
func (m Map) Set(key string, value interface{}) Map {
	m[key] = value
	return m
}

// Value holds a value retrieved from the Map.
type Value struct {
	data interface{}
}

// Get returns a Value for the provided key. When the key does not exist, the
// value will wrap nil which matches the expectations of testify/mock.
func (m Map) Get(key string) *Value {
	if m == nil {
		return &Value{}
	}
	return &Value{data: m[key]}
}

// MustInter returns the underlying data without additional checks. It mirrors
// the behaviour relied upon by testify/mock's helpers.
func (v *Value) MustInter() interface{} {
	if v == nil {
		return nil
	}
	return v.data
}
