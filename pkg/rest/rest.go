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
func CloseResponse(response *http.Response) {
	_, _ = ioutil.ReadAll(response.Body)
	response.Body.Close()
}
