//go:build !unix

package endpoint

import "errors"

func OpenEndpoint(string) (Endpoint, error) {
	return Endpoint{}, errors.New("endpoint directory requires a Unix platform")
}
