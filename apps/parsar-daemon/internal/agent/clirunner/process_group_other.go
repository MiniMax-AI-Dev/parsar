//go:build !unix

package clirunner

import "errors"

func startProcessGroup(StartOptions) (*Process, error) {
	return nil, errors.New("clirunner: process-group ownership requires Unix")
}
