package regserver

import (
	"errors"
	"fmt"
	"sync"

	"github.com/golang/glog"
	l "github.com/relab/smartMerge/leader"
	pb "github.com/relab/smartMerge/proto"
	"golang.org/x/net/context"
)

type RegServer struct {
	sync.RWMutex
	Cur     *pb.Blueprint
	CurC    uint32
	LAState *pb.Blueprint //Used only for SM-Lattice agreement
	RState  *pb.State
	Next    []*pb.Blueprint
	NextMap map[uint32]*pb.Blueprint //Used only for Consensus based
	Rnd     map[uint32]uint32        //Used only for Consensus based
	Val     map[uint32]*pb.CV        //Used only for Consensus based
	noabort bool
	Leader  *l.Leader
}

func (rs *RegServer) PrintState(op string) {
	fmt.Println("Did operation :", op)
	fmt.Println("New State:")
	fmt.Println("Cur ", rs.Cur)
	fmt.Println("CurC ", rs.CurC)
	fmt.Println("LAState ", rs.LAState)
	fmt.Println("RState ", rs.RState)
	fmt.Println("Next", rs.Next)
}

var InitState = pb.State{Value: nil, Timestamp: int32(0), Writer: uint32(0)}

func NewRegServer(noabort bool) *RegServer {
	rs := &RegServer{}
	rs.RWMutex = sync.RWMutex{}
	rs.RState = &pb.State{Value: make([]byte, 0), Timestamp: int32(0), Writer: uint32(0)}
	rs.Next = make([]*pb.Blueprint, 0, 5)
	rs.NextMap = make(map[uint32]*pb.Blueprint, 5)
	rs.Rnd = make(map[uint32]uint32, 5)
	rs.Val = make(map[uint32]*pb.CV, 5)
	rs.noabort = noabort
	return rs
}

func NewRegServerWithCur(cur *pb.Blueprint, curc uint32, noabort bool) *RegServer {
	rs := NewRegServer(noabort)
	rs.Cur = cur
	rs.CurC = curc

	return rs
}

// handleConf updates the information about
// blueprints/ configuraitons stored at the server and returns
// all configurations larger than the current one.
func (rs *RegServer) handleConf(conf *pb.Conf, n *pb.Blueprint) (cr *pb.ConfReply) {
	if conf == nil || (conf.This < rs.CurC && !rs.noabort) {
		//The client is using an outdated configuration, abort.
		return &pb.ConfReply{Cur: rs.Cur, Abort: false}
	}

	if n != nil {
		found := false
		for _, nxt := range rs.Next {
			if n.LearnedEquals(nxt) {
				found = true
				break
			}
		}
		if !found {
			rs.Next = append(rs.Next, n)
		}
	}

	next := make([]*pb.Blueprint, 0, len(rs.Next))
	this := int(conf.This)
	for _, nxt := range rs.Next {
		if nxt.Len() > this {
			next = append(next, nxt)
		}
	}

	if conf.Cur < rs.CurC {
		// Inform the client of the new current configuration
		return &pb.ConfReply{Cur: rs.Cur, Abort: false, Next: next}
	}
	if len(next) > 0 {
		// Inform the client of the next configurations
		return &pb.ConfReply{Next: next}
	}
	return nil
}

func (rs *RegServer) Read(ctx context.Context, rr *pb.Conf) (*pb.ReadReply, error) {
	rs.RLock()
	defer rs.RUnlock()
	glog.V(5).Infoln("Handling ReadS")

	cr := rs.handleConf(rr, nil)
	if cr != nil && cr.Abort {
		return &pb.ReadReply{Cur: cr}, nil
	}

	return &pb.ReadReply{State: rs.RState, Cur: cr}, nil
}

func (rs *RegServer) Write(ctx context.Context, wr *pb.WriteS) (*pb.ConfReply, error) {
	rs.Lock()
	defer rs.Unlock()
	glog.V(5).Infoln("Handling WriteS")
	if rs.RState.Compare(wr.GetState()) == 1 {
		rs.RState = wr.GetState()
	}

	if crepl := rs.handleConf(wr.GetConf(), nil); crepl != nil {
		return crepl, nil
	}
	return &pb.ConfReply{}, nil
}

func (rs *RegServer) WriteNext(ctx context.Context, wr *pb.WriteN) (*pb.WriteNReply, error) {
	rs.Lock()
	defer rs.Unlock()
	glog.V(5).Infoln("Handling WriteN")

	cr := rs.handleConf(&pb.Conf{This: wr.CurC, Cur: wr.CurC}, wr.Next)
	if cr != nil && cr.Abort {
		return &pb.WriteNReply{Cur: cr}, nil
	}

	rs.NextMap[wr.CurC] = wr.Next // This is nor necessary for sm, but only for running Consensus using norecontact.

	return &pb.WriteNReply{Cur: cr, State: rs.RState, LAState: rs.LAState}, nil
}

func (rs *RegServer) LAProp(ctx context.Context, lap *pb.LAProposal) (lar *pb.LAReply, err error) {
	rs.Lock()
	defer rs.Unlock()
	glog.V(5).Infoln("Handling LAProp")

	cr := rs.handleConf(lap.GetConf(), nil)
	if cr != nil && cr.Abort {
		return &pb.LAReply{Cur: cr}, nil
	}

	if rs.LAState.Compare(lap.Prop) == 1 {
		glog.V(6).Infoln("LAState Accepted")
		//Accept
		rs.LAState = lap.Prop
		return &pb.LAReply{Cur: cr}, nil
	}

	//Not Accepted, try again.
	rs.LAState = rs.LAState.Merge(lap.Prop)
	if cr != nil {
		// In this case, we don't need to send the next values, since the client first has to solve LA in this configuration.
		cr.Next = nil
	}
	return &pb.LAReply{Cur: cr, LAState: rs.LAState}, nil
}

func (rs *RegServer) SetState(ctx context.Context, ns *pb.NewState) (*pb.NewStateReply, error) {
	rs.Lock()
	defer rs.Unlock()
	glog.V(5).Infoln("Handling SetState")
	if ns == nil {
		return nil, errors.New("Empty NewState message")
	}

	rs.LAState = rs.LAState.Merge(ns.LAState)
	if rs.RState.Compare(ns.State) == 1 {
		rs.RState = ns.State
	}

	if rs.CurC > ns.CurC {
		return &pb.NewStateReply{Cur: rs.Cur}, nil
	}

	next := make([]*pb.Blueprint, 0, len(rs.Next))
	this := int(ns.CurC)
	for _, nxt := range rs.Next {
		if nxt.Len() > this {
			next = append(next, nxt)
		}
	}

	return &pb.NewStateReply{Next: next}, nil
}

func (rs *RegServer) GetPromise(ctx context.Context, pre *pb.Prepare) (*pb.Promise, error) {
	rs.Lock()
	defer rs.Unlock()
	glog.V(5).Infoln("Handling Prepare")

	if pre.CurC < rs.CurC {
		return &pb.Promise{Cur: rs.Cur}, nil
	}

	if rs.NextMap[pre.CurC] != nil {
		// Something was decided already
		return &pb.Promise{Dec: rs.NextMap[pre.CurC]}, nil
	}

	if rnd, ok := rs.Rnd[pre.CurC]; !ok || pre.Rnd > rnd {
		// A Prepare in a new and higher round.
		rs.Rnd[pre.CurC] = pre.Rnd
		return &pb.Promise{Val: rs.Val[pre.CurC]}, nil
	}

	return &pb.Promise{Rnd: rs.Rnd[pre.CurC], Val: rs.Val[pre.CurC]}, nil
}

func (rs *RegServer) Accept(ctx context.Context, pro *pb.Propose) (lrn *pb.Learn, err error) {
	rs.Lock()
	defer rs.Unlock()
	glog.V(5).Infoln("Handling Accept")

	if pro.CurC < rs.CurC {
		return &pb.Learn{Cur: rs.Cur}, nil
	}

	if rs.NextMap[pro.CurC] != nil {
		// This instance is decided already
		return &pb.Learn{Dec: rs.NextMap[pro.CurC]}, nil
	}

	if rs.Rnd[pro.CurC] > pro.Val.Rnd {
		// Accept in old round.
		return &pb.Learn{Learned: false}, nil
	}

	rs.Rnd[pro.CurC] = pro.Val.Rnd
	rs.Val[pro.CurC] = pro.Val
	return &pb.Learn{Learned: true}, nil
}

// Used to set the current configuration. Currenlty only used at startup.
func (rs *RegServer) SetCur(ctx context.Context, nc *pb.NewCur) (*pb.NewCurReply, error) {
	glog.V(5).Infoln("Handling Set Cur")
	rs.Lock()
	defer rs.Unlock()
	//defer rs.PrintState("SetCur")

	if nc.CurC == rs.CurC {
		return &pb.NewCurReply{New: false}, nil
	}

	if nc.Cur.LearnedCompare(rs.Cur) >= 0 {
		return &pb.NewCurReply{New: false}, nil
	}

	glog.V(3).Infoln("New Current Conf: ", nc.GetCur())
	rs.Cur = nc.Cur
	rs.CurC = nc.CurC

	newNext := make([]*pb.Blueprint, 0, len(rs.Next))
	for _, blp := range rs.Next {
		if uint32(blp.Len()) > rs.CurC {
			newNext = append(newNext, blp)
		}
	}
	rs.Next = newNext

	return &pb.NewCurReply{New: true}, nil
}

func (rs *RegServer) Fwd(ctx context.Context, p *pb.Proposal) (*pb.Ack, error) {
	if rs.Leader == nil {
		glog.Errorln("Received Fwd request but have no leader.")
		return nil, errors.New("Not implemented.")
	}
	glog.V(4).Infoln("Handling Reconf Proposal")
	rs.Leader.Propose(p.GetProp())
	return &pb.Ack{}, nil
}

func (rs *RegServer) AddLeader(leader *l.Leader) {
	rs.Lock()
	defer rs.Unlock()
	rs.Leader = leader
}
