package rest

import (
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
)

// ReadBody reads body from a ReadCloser and returns it as a string
func ReadBody(body io.ReadCloser) string {
	buf := new(bytes.Buffer)
	buf.ReadFrom(body)
	return buf.String()
}

// CloseResponse reads the body and close the response. To be used to prevent file descriptor leaks.
func CloseResponse(response *http.Response) {
	ioutil.ReadAll(response.Body)
	response.Body.Close()
}
