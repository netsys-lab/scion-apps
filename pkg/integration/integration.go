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

package integration

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/scionproto/scion/go/lib/addr"
	"github.com/scionproto/scion/go/lib/common"
	"github.com/scionproto/scion/go/lib/log"
	"github.com/scionproto/scion/go/lib/serrors"
	"github.com/scionproto/scion/go/lib/sciond"
	"github.com/scionproto/scion/go/lib/snet"
	"github.com/scionproto/scion/go/lib/snet/addrutil"
	sintegration "github.com/scionproto/scion/go/lib/integration"
)

const (
	// SCIOND is a placeholder for the SCIOND server in the arguments.
	SCIOND = "<SCIOND>"
	// ServerPortReplace is a placeholder for the server port in the arguments.
	ServerPortReplace = "<ServerPort>"
	// SrcIAReplace is a placeholder for the source IA in the arguments.
	SrcIAReplace = "<SRCIA>"
	// SrcHostReplace is a placeholder for the source host in the arguments.
	SrcHostReplace = "<SRCHost>"
	// SrcAddrPattern is a placeholder for the source address in the arguments.
	SrcAddrPattern = SrcIAReplace + ",[" + SrcHostReplace + "]"
	// DstIAReplace is a placeholder for the destination IA in the arguments.
	DstIAReplace = "<DSTIA>"
	// DstHostReplace is a placeholder for the destination host in the arguments.
	DstHostReplace = "<DSTHost>"
	// DstAddrPattern is a placeholder for the destination address in the arguments.
	DstAddrPattern = DstIAReplace + ",[" + DstHostReplace + "]"
	// ReadySignal should be written to Stdout by the server once it is read to accept clients.
	// The message should always be `Listening ia=<IA>`
	// where <IA> is the IA the server is listening on.
	//ReadySignal = "Listening ia="
	ReadySignal = "Registered with dispatcher\" addr="
	// GoIntegrationEnv is an environment variable that is set for the binary under test.
	// It can be used to guard certain statements, like printing the ReadySignal,
	// in a program under test.
	GoIntegrationEnv = "SCION_GO_INTEGRATION"
	// portString is the string a server prints to specify the port it's listening on.
	portString = "Port="
	// WrapperCmd is the command used to run non-test binaries
	WrapperCmd = "./integration/bin_wrapper.sh"

	// Default client startup timeout
	DefaultClientTimeout = 10 * time.Second
)

var (
	// FIXME(roosd): The caller to StartServer and StartClient
	// should take care of aggregating the data. I would prefer not to use a
	// global here.
	serverPortsMtx sync.Mutex
	serverPorts    = make(map[addr.IA]string)
)

var _ sintegration.Integration = (*ScionAppsIntegration)(nil)

type ScionAppsIntegration struct {
	name        string
	cmd         string
	clientArgs  []string
	serverArgs  []string
	logDir      string
	outMatchFun func(previous bool, stdout string) bool
	errMatchFun func(previous bool, stdstderrr string) bool
}

// NewAppsIntegration returns an implementation of the Integration interface.
// Start* will run the binary program with name and use the given arguments for the client/server.
// Use SrcIAReplace and DstIAReplace in arguments as placeholder for the source and destination IAs.
// When starting a client/server the placeholders will be replaced with the actual values.
// The server should output the ReadySignal to Stdout once it is ready to accept clients.
func NewAppsIntegration(name string, cmd string, clientArgs, serverArgs []string, logDir string) sintegration.Integration {
	if logDir != "" {
		logDir = fmt.Sprintf("%s/%s", logDir, name)
		err := os.Mkdir(logDir, os.ModePerm)
		if err != nil && !os.IsExist(err) {
			log.Error("Failed to create log folder for testrun", "dir", name, "err", err)
			return nil
		}
	}
	sai := &ScionAppsIntegration{
		name:       name,
		cmd:        cmd,
		clientArgs: clientArgs,
		serverArgs: serverArgs,
		logDir:     logDir,
	}
	return sai
}

func (sai *ScionAppsIntegration) SetStdoutMatchFunction(outMatchFun func(bool, string) bool) {
	sai.outMatchFun = outMatchFun
}

func (sai *ScionAppsIntegration) SetStderrMatchFunction(errMatchFun func(bool, string) bool) {
	sai.errMatchFun = errMatchFun
}

func (sai *ScionAppsIntegration) Name() string {
	return sai.name
}

// StartServer starts a server and blocks until the ReadySignal is received on Stdout.
func (sai *ScionAppsIntegration) StartServer(ctx context.Context, dst *snet.UDPAddr) (sintegration.Waiter, error) {
	args := replacePattern(DstIAReplace, dst.IA.String(), sai.serverArgs)
	args = replacePattern(DstHostReplace, dst.Host.IP.String(), args)

	sciondAddr, err := sintegration.GetSCIONDAddress(sintegration.SCIONDAddressesFile, dst.IA)
	if err != nil {
		return nil, serrors.WrapStr("unable to determine SCIOND address", err)
	}
	args = replacePattern(SCIOND, sciondAddr, args)

	r := &appsWaiter{
		exec.CommandContext(ctx, sai.cmd, args...),
		make(chan bool, 1),
		make(chan bool, 1),
	}
	log.Debug(fmt.Sprintf("Running server command: %v %v\n", sai.cmd, strings.Join(args, " ")))
	r.Env = os.Environ()
	r.Env = append(r.Env, fmt.Sprintf("%s=1", GoIntegrationEnv))
	r.Env = append(r.Env, fmt.Sprintf("SCION_DAEMON_ADDRESS=%s", sciondAddr))
	/*ep, err := r.StderrPipe()
	if err != nil {
		return nil, err
	}*/
	sp, err := r.StdoutPipe()
	if err != nil {
		return nil, err
	}
	ready := make(chan struct{})
	// parse until we have the ready signal.
	// and then discard the output until the end (required by StdoutPipe).
	go func() {
		defer log.HandlePanic()
		defer sp.Close()
		signal := fmt.Sprintf("%s%s", ReadySignal, dst.IA)
		var stdoutMatch, stderrMatch bool
		init := true
		scanner := bufio.NewScanner(sp)
		for scanner.Scan() {
			if scanner.Err() != nil {
				log.Error("Error during reading of stdout", "err", scanner.Err())
				return
			}
			line := scanner.Text()
			if strings.HasPrefix(line, portString) {
				serverPortsMtx.Lock()
				serverPorts[dst.IA] = strings.TrimPrefix(line, portString)
				serverPortsMtx.Unlock()
			}
			if init && strings.Contains(line, signal) {
				close(ready)
				init = false
			}
			if sai.outMatchFun != nil {
				stdoutMatch = sai.outMatchFun(stdoutMatch, line)
			}
			if sai.errMatchFun != nil {
				stderrMatch = sai.errMatchFun(stderrMatch, line)
			}
			log.Debug("Server stdout", "log line", fmt.Sprintf("%s", line))
		}
		if sai.outMatchFun != nil {
			r.stdoutMatch <- stdoutMatch
		} else {
			r.stdoutMatch <- true
		}
		if sai.errMatchFun != nil {
			r.stderrMatch <- stderrMatch
		} else {
			r.stderrMatch <- true
		}
	}()

	if err = r.Start(); err != nil {
		return nil, common.NewBasicError("Failed to start server", err, "dst", dst.IA)
	}
	select {
	case <-ready:
		return r, err
	case <-time.After(sintegration.StartServerTimeout):
		return nil, common.NewBasicError("Start server timed out", nil, "dst", dst.IA)
	}
}

type outOKContextKey string

// StartServer runs a server. The server can be stopped by calling Close() on the returned Closer.
// We are using a custom context to inspect the result of the output check.
func StartServer(in sintegration.Integration, dst *snet.UDPAddr) (io.Closer, error) {
	serverCtx, serverCancel := context.WithCancel(context.Background())
	stdKey := outOKContextKey("stdout")
	serverCtx = context.WithValue(serverCtx, stdKey, false)
	s, err := in.StartServer(serverCtx, dst)
	if err != nil {
		serverCancel()
		return nil, err
	}
	switch a := s.(type) {
	case *appsWaiter:
		return &serverStop{serverCancel, *a}, nil
	default:
		return  nil, errors.New("ERROR ERROR") // TODO: FIXME
	}

	//return &serverStop{serverCancel, s}, nil
}

func (sai *ScionAppsIntegration) StartClient(ctx context.Context,
	src, dst *snet.UDPAddr) (sintegration.Waiter, error) {

	args := replacePattern(SrcIAReplace, src.IA.String(), sai.clientArgs)
	args = replacePattern(SrcHostReplace, src.Host.IP.String(), args)
	args = replacePattern(DstIAReplace, dst.IA.String(), args)
	args = replacePattern(DstHostReplace, dst.Host.IP.String(), args)
	args = replacePattern(ServerPortReplace, serverPorts[dst.IA], args)

	sciond, err := sintegration.GetSCIONDAddress(sintegration.SCIONDAddressesFile, src.IA)
	if err != nil {
		return nil, serrors.WrapStr("unable to determine SCIOND address", err)
	}
	args = replacePattern(SCIOND, sciond, args)

	r := &appsWaiter{
		exec.CommandContext(ctx, sai.cmd, args...),
		make(chan bool, 1),
		make(chan bool, 1),
	}
	log.Debug(fmt.Sprintf("Running client command: %v %v\n", sai.cmd, strings.Join(args, " ")))
	r.Env = os.Environ()
	r.Env = append(r.Env, fmt.Sprintf("%s=1", GoIntegrationEnv))
	r.Env = append(r.Env, fmt.Sprintf("SCION_DAEMON_ADDRESS=%s", sciond))
	/*ep, err := r.StderrPipe()
	if err != nil {
		return nil, err
	}*/
	sp, err := r.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if sai.logDir != "" {
		fmt.Println("Log directory:", sai.logDir)
	}

	go func() {
		scanner := bufio.NewScanner(sp)
		for scanner.Scan() {
			if scanner.Err() != nil {
				log.Error("Error during reading of stdout", "err", scanner.Err())
			}
			line := scanner.Text()
			log.Debug("Client stdout", "msg", line)
		}
	}()

	return r, r.Start()
}

// RunClient runs a client on the given IAPair.
// If the client does not finish until timeout it is killed.
func RunClient(in sintegration.Integration, pair sintegration.IAPair, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c, err := in.StartClient(ctx, pair.Src, pair.Dst)
	if err != nil {
		return err
	}
	if err = c.Wait(); err != nil {
		return err
	}
	return nil
}

// RunTests runs the client and server for each IAPair.
// In case of an error the function is terminated immediately.
func RunTests(in sintegration.Integration, pairs []sintegration.IAPair, clientTimeout time.Duration) error {
	defer log.HandlePanic()
	defer log.Flush()
	return sintegration.ExecuteTimed(in.Name(), func() (clossingErr error) {
		// First run all servers
		dsts := sintegration.ExtractUniqueDsts(pairs)
		var serverCloser []io.Closer
		defer func(*[]io.Closer) {
			initialClosingErr := clossingErr
			var firstError error
			for _, c := range serverCloser {
				closerError := c.Close()
				if firstError == nil && closerError != nil {
					firstError = closerError
				}
			}
			if initialClosingErr == nil {
				clossingErr = firstError
			}
		}(&serverCloser)
		for _, dst := range dsts {
			c, err := StartServer(in, dst)
			if err != nil {
				log.Error(fmt.Sprintf("Error in server: %s", dst.String()), "err", err)
				return err
			}
			serverCloser = append(serverCloser, c)
		}
		// Now start the clients for srcDest pair
		for i, conn := range pairs {
			testInfo := fmt.Sprintf("%v -> %v (%v/%v)", conn.Src.IA, conn.Dst.IA, i+1, len(pairs))
			log.Info(fmt.Sprintf("Test %v: %s", in.Name(), testInfo))
			if err := RunClient(in, conn, clientTimeout); err != nil {
				log.Error(fmt.Sprintf("Error in client: %s", testInfo), "err", err)
				return err
			}
		}
		return nil
	})
}

func findSciond(ctx context.Context, sciondAddress string) (sciond.Connector, error) {
	sciondConn, err := sciond.NewService(sciondAddress).Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to SCIOND at %s (override with SCION_DAEMON_ADDRESS): %w", sciondAddress, err)
	}
	return sciondConn, nil
}

// findAnyHostInLocalAS returns the IP address of some (infrastructure) host in the local AS.
func findAnyHostInLocalAS(ctx context.Context, sciondConn sciond.Connector) (net.IP, error) {
	addr, err := sciond.TopoQuerier{Connector: sciondConn}.OverlayAnycast(ctx, addr.SvcBS)
	if err != nil {
		return nil, err
	}
	return addr.IP, nil
}

func DefaultLocalIPAddress(sciondAddress string) (net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sciondConn, err := findSciond(ctx, sciondAddress)
	if err != nil {
		return nil, err
	}
	hostInLocalAS, err := findAnyHostInLocalAS(ctx, sciondConn)
	if err != nil {
		return nil, err
	}
	return addrutil.ResolveLocal(hostInLocalAS)
}

func replacePattern(pattern string, replacement string, args []string) []string {
	// first copy
	argsCopy := append([]string(nil), args...)
	for i, arg := range argsCopy {
		if strings.Contains(arg, pattern) {
			argsCopy[i] = strings.Replace(arg, pattern, replacement, -1)
		}
	}
	return argsCopy
}

func clientId(src, dst *snet.UDPAddr) string {
	return fmt.Sprintf("%s_%s", src.IA.FileFmt(false), dst.IA.FileFmt(false))
}

var _ sintegration.Waiter = (*appsWaiter)(nil)

type appsWaiter struct {
	*exec.Cmd
	stdoutMatch chan bool
	stderrMatch chan bool
}

type serverStop struct {
	cancel context.CancelFunc
	wait   appsWaiter
}

func checkOutputMatches(stdoutRes chan bool, stderrRes chan bool) error {
	result, ok := <- stdoutRes
	if ok {
		if !result {
			return errors.New("the program under test did not produce the expected standard output")
		}
	}
	result, ok = <- stderrRes
	if ok {
		if !result {
			return errors.New("command did not produce the expected error output")
		}
	}
	return nil
}

func (s *serverStop) Close() error {
	s.cancel()
	s.wait.Process.Wait()
	err := checkOutputMatches(s.wait.stdoutMatch, s.wait.stderrMatch)
	if err != nil {
		return err
	}
	err = s.wait.Wait()
	_ = err
	return nil
}

// Init initializes the integration test, it adds and validates the command line flags,
// and initializes logging.
func Init(name string) error {
	return sintegration.Init(name)
}

func getSCIONDAddress(ia addr.IA) (string, error) {
	networksFile := sintegration.SCIONDAddressesFile
	return sintegration.GetSCIONDAddress(networksFile, ia)
}

// IAPairs returns all IAPairs that should be tested.
func IAPairs(hostAddr sintegration.HostAddr) []sintegration.IAPair {
	if hostAddr == nil {
		log.Error("Failed to get IAPairs", "err", "hostAddr is nil")
		return []sintegration.IAPair{}
	}
	return sintegration.IAPairs(hostAddr)
}

// DispAddr reads the CS host Addr from the topology for the specified IA. In general this
// could be the IP of any service (PS/BS/CS) in that IA because they share the same dispatcher in
// the dockerized topology.
// The host IP is used as client or server address in the tests because the testing container is
// connecting to the dispatcher of the services.
var DispAddr sintegration.HostAddr = sintegration.DispAddr

// HostAddr gets _a_ host address, the same way appnet does, for a given IA
var HostAddr sintegration.HostAddr = func(ia addr.IA) *snet.UDPAddr {
	sciond, err := getSCIONDAddress(ia)
	hostIP, err := DefaultLocalIPAddress(sciond)
	if err != nil {
		log.Error("Failed to get valid host IP", "err", err)
		return nil
	}
	return &snet.UDPAddr{IA: ia, Host: &net.UDPAddr{IP: hostIP, Port: 0}}
}

