package checker_test

import "testing"

func TestOutputInLibraryPackageIsRejected(t *testing.T) {
	_, errs := checkMarkStub(t, "output {\n    go {\n        bubbletea()\n    }\n}\n")
	wantMarkErr(t, errs, "sngl:markstub cannot declare output")
}
