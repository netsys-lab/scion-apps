package mpsquic


import (
	"context"
	"fmt"
	"time"

	"github.com/scionproto/scion/go/lib/addr"
	"github.com/scionproto/scion/go/lib/common"
	"github.com/scionproto/scion/go/lib/pathmgr"
	"github.com/scionproto/scion/go/lib/sciond"
	"github.com/scionproto/scion/go/lib/scmp"
	"github.com/scionproto/scion/go/lib/snet"
	"github.com/scionproto/scion/go/lib/sock/reliable"
)

type scmpHandler struct {
	// pathResolver manages revocations received via SCMP. If nil, nothing is informed.
	pathResolver pathmgr.Resolver
}

func (h *scmpHandler) Handle(pkt *snet.SCIONPacket) error {
	hdr, ok := pkt.L4Header.(*scmp.Hdr)
	if !ok {
		return common.NewBasicError("scmp handler invoked with non-scmp packet", nil, "pkt", pkt)
	}

	// Only handle revocations for now
	if hdr.Class == scmp.C_Path && hdr.Type == scmp.T_P_RevokedIF {
		return h.handleSCMPRev(hdr, pkt)
	}
	fmt.Println("Ignoring scmp packet", "hdr", hdr, "src", pkt.Source)
	return nil
}

func (h *scmpHandler) handleSCMPRev(hdr *scmp.Hdr, pkt *snet.SCIONPacket) error {
	scmpPayload, ok := pkt.Payload.(*scmp.Payload)
	if !ok {
		return common.NewBasicError("Unable to type assert payload to SCMP payload", nil,
			"type", common.TypeOf(pkt.Payload))
	}
	info, ok := scmpPayload.Info.(*scmp.InfoRevocation)
	if !ok {
		return common.NewBasicError("Unable to type assert SCMP Info to SCMP Revocation Info", nil,
			"type", common.TypeOf(scmpPayload.Info))
	}
	fmt.Println("Received SCMP revocation", "header", hdr.String(), "payload", scmpPayload.String(),
		"src", pkt.Source)
	if h.pathResolver != nil {
		h.pathResolver.RevokeRaw(context.TODO(), info.RawSRev)
	}
	// Path has been r
	return nil
}

type Error interface {
	error
	SCMP() *scmp.Hdr
}

var _ Error = (*OpError)(nil)

type OpError struct {
	scmp *scmp.Hdr
}

func (e *OpError) SCMP() *scmp.Hdr {
	return e.scmp
}

func (e *OpError) Error() string {
	return e.scmp.String()
}

// initNetworkWithPRCustomSCMPHandler user the default snet DefaultPacketDispatcherService, but
// with a custom SCMP handler
func initNetworkWithPRCustomSCMPHandler(ia addr.IA, sciondPath string, dispatcher reliable.DispatcherService) error {
	pathResolver, err := getResolver(sciondPath)
	if err != nil {
		return err
	}
	network := snet.NewCustomNetworkWithPR(ia,
		&snet.DefaultPacketDispatcherService{
			Dispatcher: dispatcher,
			SCMPHandler: &scmpHandler{
				pathResolver: pathResolver,
			},
		},
		pathResolver,
	)
	return snet.InitWithNetwork(network)
}

// getResolver builds a default resolver for mpsquic internals.
func getResolver(sciondPath string) (pathmgr.Resolver, error) {
	var pathResolver pathmgr.Resolver
	if sciondPath != "" {
		sciondConn, err := sciond.NewService(sciondPath, true).Connect()
		if err != nil {
			return nil, common.NewBasicError("Unable to initialize SCIOND service", err)
		}
		pathResolver = pathmgr.New(
			sciondConn,
			pathmgr.Timers{
				NormalRefire: time.Minute,
				ErrorRefire:  3 * time.Second,
			},
		)
	}
	return pathResolver, nil
}
