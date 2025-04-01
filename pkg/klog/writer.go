package klog

import (
	"io"

	klogv2 "k8s.io/klog/v2"
)

// OutputCallDepth is the stack depth where we can find the origin of this call
const OutputCallDepth = 6

// DefaultPrefixLength is the length of the log prefix that we have to strip out
const DefaultPrefixLength = 53

// Writer is used in SetOutputBySeverity call below to redirect
// any calls to klogv1 to end up in klogv2
type Writer struct{}

var _ io.Writer = Writer{}

func (kw Writer) Write(p []byte) (n int, err error) { // nolint:unparam
	if len(p) < DefaultPrefixLength {
		klogv2.InfoDepth(OutputCallDepth, string(p))
		return len(p), nil
	}
	switch p[0] {
	case 'I':
		klogv2.InfoDepth(OutputCallDepth, string(p[DefaultPrefixLength:]))
	case 'W':
		klogv2.WarningDepth(OutputCallDepth, string(p[DefaultPrefixLength:]))
	case 'E':
		klogv2.ErrorDepth(OutputCallDepth, string(p[DefaultPrefixLength:]))
	case 'F':
		klogv2.FatalDepth(OutputCallDepth, string(p[DefaultPrefixLength:]))
	default:
		klogv2.InfoDepth(OutputCallDepth, string(p[DefaultPrefixLength:]))
	}
	return len(p), nil
}
