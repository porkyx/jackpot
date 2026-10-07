package desktop

import "reflect"

// An interface holding a nil pointer or function is still non-nil. Reject it
// during construction so a broken dependency cannot panic during an IPC call.
func nilDependency(value interface{}) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
