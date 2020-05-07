// Copyright 2020 ETH Zurich
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// +build scion_integration

package main

import (
	"strings"
	"testing"

	"github.com/netsec-ethz/scion-apps/pkg/integration"
)

const (
	name = "helloworld"
	cmd  = "helloworld"
)

func TestHelloworldSample(t *testing.T) {
	if err := integration.Init(name); err != nil {
		t.Fatalf("Failed to init: %s\n", err)
	}
	// Common arguments
	cmnArgs := []string{}
	// Server
	serverPort := "12345"
	serverArgs := []string{"-port", serverPort}
	serverArgs = append(serverArgs, cmnArgs...)

	testCases := []struct {
		Name string
		Args []string
		ServerOutMatchFun func(bool, string) bool
		ServerErrMatchFun func(bool, string) bool
		ClientOutMatchFun func(bool, string) bool
		ClientErrMatchFun func(bool, string) bool
	}{
		{
			"client_hello",
			append([]string{"-remote", integration.DstAddrPattern + ":" + serverPort}, cmnArgs...),
			func (prev bool, line string) bool {
				res := strings.Contains(line, "hello world") //
				return prev || res // return true if any output line contains the string
			},
			nil,
			func (prev bool, line string) bool {
				res := strings.Contains(line, "Done. Wrote 11 bytes.")
				return prev || res // return true if any output line contains the string
			},
			nil,
		},
		{
			"client_error",
			append([]string{"-remote", "1-ff00:0:bad,[127.0.0.1]:" + serverPort}, cmnArgs...),
			nil,
			nil,
			nil,
			func (prev bool, line string) bool {
				res := strings.Contains(line, "No paths available")
				return prev || res // return true if any error line contains the string
			},
		},
	}

	for _, tc := range testCases {
		in := integration.NewAppsIntegration(name, tc.Name, cmd, tc.Args, serverArgs, true)
		in.ServerStdout(tc.ServerOutMatchFun)
		in.ClientStdout(tc.ClientOutMatchFun)
		// Host address pattern
		hostAddr := integration.HostAddr
		// Cartesian product of src and dst IAs, is a random permutation
		// can be restricted to a subset to reduce the number of tests to run without significant
		// loss of coverage
		IAPairs := integration.IAPairs(hostAddr)[:5]
		// Run the tests to completion or until a test fails,
		// increase the client timeout if clients need more time to start
		if err := integration.RunTests(in, IAPairs, integration.DefaultClientTimeout); err != nil {
			t.Fatalf("Error during tests err: %v", err)
		}
	}
}

