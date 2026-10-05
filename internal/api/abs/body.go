package abs

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

var errTrailingJSON = errors.New("trailing json")

// decodeBody enforces the bounded, single-document JSON contract for ABS
// request bodies. An empty body stays a valid zero value (clients that POST
// play/close without a payload relied on it); anything malformed, trailing,
// or over max is rejected instead of falling through as zero-value fields.
func decodeBody(w http.ResponseWriter, r *http.Request, max int64, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	dec := json.NewDecoder(r.Body)
	err := dec.Decode(dst)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errTrailingJSON
		}
		return err
	}
	return nil
}

func bodyErrorStatus(err error) (int, string) {
	var large *http.MaxBytesError
	if errors.As(err, &large) {
		return http.StatusRequestEntityTooLarge, "Request body too large"
	}
	return http.StatusBadRequest, "Invalid body"
}
