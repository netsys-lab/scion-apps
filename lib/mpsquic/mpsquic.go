// Copyright 2019 ETH Zurich
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

// "Multiple paths" QUIC/SCION implementation.
package mpsquic

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/lucas-clemente/quic-go"
	"github.com/scionproto/scion/go/lib/addr"
	"github.com/scionproto/scion/go/lib/common"
	"github.com/scionproto/scion/go/lib/hostinfo"
	"github.com/scionproto/scion/go/lib/overlay"
	"github.com/scionproto/scion/go/lib/sciond"
	"github.com/scionproto/scion/go/lib/snet"
	"github.com/scionproto/scion/go/lib/sock/reliable"
	"github.com/scionproto/scion/go/lib/spath"
	"github.com/scionproto/scion/go/lib/spath/spathmeta"
)

const (
	defKeyPath = "gen-certs/tls.key"
	defPemPath = "gen-certs/tls.pem"
)

const (
	maxDuration time.Duration = 1<<63 - 1
)

var _ quic.Session = (*MPQuic)(nil)

type pathInfo struct {
	raddr      *snet.Addr
	path       spathmeta.AppPath
	expiration time.Time
	rtt        time.Duration
	bw         int // in bps
}

type MPQuic struct {
	quic.Session
	scionFlexConnection *SCIONFlexConn
	network             *snet.SCIONNetwork
	dispConn            *reliable.Conn
	paths               []pathInfo
	active              *pathInfo
}

var (
	// Don't verify the server's cert, as we are not using the TLS PKI.
	cliTlsCfg = &tls.Config{InsecureSkipVerify: true}
	srvTlsCfg = &tls.Config{}
)

// Init initializes initializes the SCION networking context and the QUIC session's crypto.
func Init(ia addr.IA, sciondPath, dispatcher , keyPath, pemPath string) error {
	/*if err := snet.Init(ia, sciondPath, reliable.NewDispatcherService(dispatcher)); err != nil {
		return common.NewBasicError("mpsquic: Unable to initialize SCION network", err)
	}*/
	if err := initNetworkWithPRCustomSCMPHandler(ia, sciondPath, reliable.NewDispatcherService(dispatcher)); err != nil {
		return common.NewBasicError("mpsquic: Unable to initialize SCION network", err)
	}
	if keyPath == "" {
		keyPath = defKeyPath
	}
	if pemPath == "" {
		pemPath = defPemPath
	}
	cert, err := tls.LoadX509KeyPair(pemPath, keyPath)
	if err != nil {
		return common.NewBasicError("mpsquic: Unable to load TLS cert/key", err)
	}
	srvTlsCfg.Certificates = []tls.Certificate{cert}
	return nil
}

// OpenStreamSync opens a QUIC stream over the QUIC session.
// It returns a QUIC stream ready to be written/read.
func (mpq *MPQuic) OpenStreamSync() (quic.Stream, error) {
	stream, err := mpq.Session.OpenStreamSync()
	if err != nil {
		return nil, err
	}
	return monitoredStream{stream, mpq}, nil
}

// Close closes the QUIC session.
func (mpq *MPQuic) Close(err error) error {
	if mpq.Session != nil {
		return mpq.Session.Close(err)
	}
	return nil
}

// CloseConn closes the embedded SCION connection.
func (mpq *MPQuic) CloseConn() error {
	if mpq.dispConn != nil {
		tmp := mpq.dispConn
		mpq.dispConn = nil
		time.Sleep(time.Second)
		err := tmp.Close()
		if err != nil {
			return err
		}
	}
	return mpq.scionFlexConnection.Close()
}

// DialMP creates a monitored multiple paths connection using QUIC over SCION.
// It returns a MPQuic struct if opening a QUIC session over the initial SCION path succeeded.
func DialMP(network *snet.SCIONNetwork, laddr *snet.Addr, raddr *snet.Addr, paths *[]spathmeta.AppPath,
	quicConfig *quic.Config) (*MPQuic, error) {

	return DialMPWithBindSVC(network, laddr, raddr, paths, nil, addr.SvcNone, quicConfig)
}

// createSCMPMonitorConn opens a connection to the default dispatcher.
// It returns a reliable socket connection.
func createSCMPMonitorConn(laddr, baddr *snet.Addr) (dispConn *reliable.Conn, err error) {
	// Connect to the dispatcher
	var overlayBindAddr *overlay.OverlayAddr
	if baddr != nil {
		if baddr.Host != nil {
			overlayBindAddr, err = overlay.NewOverlayAddr(baddr.Host.L3, baddr.Host.L4)
			if err != nil {
				return nil, errors.New(fmt.Sprintf("Failed to create bind address: %v\n", err))
			}
		}
	}
	laddrMonitor := laddr.Copy()
	laddrMonitor.Host.L4 = addr.NewL4UDPInfo(laddr.Host.L4.Port() + 1)
	dispConn, _, err = reliable.Register(reliable.DefaultDispPath, laddrMonitor.IA, laddrMonitor.Host,
		overlayBindAddr, addr.SvcNone)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("Unable to register with the dispatcher addr=%s\nerr=%v", laddrMonitor, err))
	}
	return dispConn, err
}

// Creates an AppPath using the spath.Path and hostinfo addr.AppAddr available on a snet.Addr, missing values are set to their zero value.
func mockAppPath(spathP *spath.Path, host *addr.AppAddr) (appPath *spathmeta.AppPath, err error) {
	appPath = &spathmeta.AppPath{
		Entry: &sciond.PathReplyEntry{
			Path:     nil,
			HostInfo: *hostinfo.FromHostAddr(addr.HostFromIPStr("127.0.0.1"), 30041)}}
	if host != nil {
		appPath.Entry.HostInfo = *hostinfo.FromHostAddr(host.L3, host.L4.Port())
	}
	if spathP == nil {
		return appPath, nil
	}

	cpath, err := parseSPath(*spathP)
	if err != nil {
		return nil, err
	}

	appPath.Entry.Path = &sciond.FwdPathMeta{
		FwdPath:    spathP.Raw,
		Mtu:        cpath.Mtu,
		Interfaces: cpath.Interfaces,
		ExpTime:    uint32(cpath.ComputeExpTime().Unix())}
	return appPath, nil
}

// DialMPWithBindSVC creates a monitored multiple paths connection using QUIC over SCION on the specified bind address baddr.
// It returns a MPQuic struct if a opening a QUIC session over the initial SCION path succeeded.
func DialMPWithBindSVC(network *snet.SCIONNetwork, laddr *snet.Addr, raddr *snet.Addr, paths *[]spathmeta.AppPath, baddr *snet.Addr,
	svc addr.HostSVC, quicConfig *quic.Config) (*MPQuic, error) {

	if network == nil {
		network = snet.DefNetwork
	}

	sconn, err := sListen(network, laddr, baddr, svc)
	if err != nil {
		return nil, err
	}

	dispConn, err := createSCMPMonitorConn(laddr, baddr)
	if err != nil {
		return nil, err
	}

	if paths == nil {
		paths = &[]spathmeta.AppPath{}
		// Infer path meta information from path on raddr, since no paths were provided
		appPath, err := mockAppPath(raddr.Path, raddr.Host)
		if err != nil {
			return nil, err
		}
		*paths = append(*paths, *appPath)
	}

	var raddrs []*snet.Addr = []*snet.Addr{}
	// Initialize a raddr for each path
	for _, p := range *paths {
		r := raddr.Copy()
		if p.Entry.Path != nil {
			r.Path = spath.New(p.Entry.Path.FwdPath)
		}
		if r.Path != nil {
			_ = r.Path.InitOffsets()
		}
		r.NextHop, _ = p.Entry.HostInfo.Overlay()
		raddrs = append(raddrs, r)
	}

	pathInfos := []pathInfo{}
	for i, raddr := range raddrs {
		pi := pathInfo{
			raddr:      raddr,
			path:       (*paths)[i],
			expiration: time.Time{},
			rtt:        maxDuration,
			bw:         0,
		}
		pathInfos = append(pathInfos, pi)
	}

	mpQuic, err := newMPQuic(sconn, laddr, network, quicConfig, dispConn, pathInfos)
	if err != nil {
		return nil, err
	}
	mpQuic.monitor()

	return mpQuic, nil
}

func newMPQuic(sconn snet.Conn, laddr *snet.Addr, network *snet.SCIONNetwork, quicConfig *quic.Config, dispConn *reliable.Conn, pathInfos []pathInfo) (mpQuic *MPQuic, err error) {
	active := &pathInfos[0]
	mpQuic = &MPQuic{Session: nil, scionFlexConnection: nil, network: network, dispConn: dispConn, paths: pathInfos, active: active}
	fmt.Printf("Active path key: %v,\tHops: %v\n", active.path.Key(), active.path.Entry.Path.Interfaces)
	flexConn := newSCIONFlexConn(sconn, mpQuic, laddr, active.raddr)
	mpQuic.scionFlexConnection = flexConn

	// Use dummy hostname, as it's used for SNI, and we're not doing cert verification.
	qsession, err := quic.Dial(flexConn, flexConn.raddr, "host:0", cliTlsCfg, quicConfig)
	if err != nil {
		return nil, err
	}
	mpQuic.Session = qsession
	return mpQuic, nil
}

// displayStats prints the collected metrics for all monitored paths.
func (mpq *MPQuic) displayStats() {
	for i, pathInfo := range mpq.paths {
		fmt.Printf("Path %v will expire at %v.\n", i, pathInfo.expiration)
		fmt.Printf("Measured RTT of %v on path %v.\n", pathInfo.rtt, i)
		fmt.Printf("Measured approximate BW of %v Mbps on path %v.\n", pathInfo.bw/1e6, i)
	}
}

// policyLowerRTTMatch returns true if the path with candidate index has a lower RTT than the active path.
func (mpq *MPQuic) policyLowerRTTMatch(candidate int) bool {
	return mpq.paths[candidate].rtt < mpq.active.rtt
}

// updateActivePath updates the active path in a thread safe manner.
func (mpq *MPQuic) updateActivePath(newPathIndex int) {
	// Lock the connection raddr, and update both the active path and the raddr of the FlexConn.
	mpq.scionFlexConnection.addrMtx.Lock()
	defer mpq.scionFlexConnection.addrMtx.Unlock()
	mpq.active = &mpq.paths[newPathIndex]
	mpq.scionFlexConnection.setRemoteAddr(mpq.active.raddr)
}

// switchMPConn switches between different SCION paths as given by the SCION address with path structs in paths.
// The force flag makes switching a requirement, set it when continuing to use the existing path is not an option.
func (mpq *MPQuic) switchMPConn(force bool, filter bool) error {
	if _, set := os.LookupEnv("DEBUG"); set { // TODO: Remove this when cleaning up logging
		mpq.displayStats()
	}
	if force {
		// Always refresh available paths, as failing to find a fresh path leads to a hard failure
		mpq.refreshPaths(mpq.network.PathResolver())
	}
	for i := range mpq.paths {
		// Do not switch to identical path or to expired path
		if mpq.scionFlexConnection.raddr != mpq.paths[i].raddr && mpq.paths[i].expiration.After(time.Now()) {
			// fmt.Printf("Previous path: %v\n", mpq.scionFlexConnection.raddr.Path)
			// fmt.Printf("New path: %v\n", mpq.paths[i].raddr.Path)
			if !filter {
				mpq.updateActivePath(i)
				fmt.Printf("Updating to path %d\n", i)
				return nil
			}
			if mpq.policyLowerRTTMatch(i) {
				mpq.updateActivePath(i)
				fmt.Printf("Updating to better path %d\n", i)
				return nil
			}
		}
	}
	if !force {
		return nil
	}
	fmt.Printf("No path available now %v\n", time.Now())
	mpq.displayStats()

	return common.NewBasicError("mpsquic: No fallback connection available.", nil)
}