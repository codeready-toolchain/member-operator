package rest

import (
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
)

// ReadBody reads body from a ReadCloser and returns it as a string
func ReadBody(body io.ReadCloser) (string, error) {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(body)
	return buf.String(), err
}

// CloseResponse reads the body and close the response. To be used to prevent file descriptor leaks.
func CloseResponse(res *http.Response) {
	if res != nil {
		io.Copy(ioutil.Discard, res.Body) //nolint: errcheck
		defer res.Body.Close()
	}
}
