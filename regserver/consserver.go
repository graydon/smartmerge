package regserver

import (
	"errors"
	"fmt"
	"sync"

	pb "github.com/relab/smartMerge/proto"
	"golang.org/x/net/context"
)

type ConsServer struct {
	Cur    *pb.Blueprint
	CurC   uint32
	RState *pb.State
	Next   map[uint32]*pb.Blueprint
	Rnd    map[uint32]uint32
	Val    map[uint32]*pb.CV
	mu     sync.RWMutex
}

func (cs *ConsServer) PrintState(op string) {
	fmt.Println("Did operation :", op)
	fmt.Println("New State:")
	fmt.Println("Cur ", cs.Cur)
	fmt.Println("CurC ", cs.CurC)
	fmt.Println("RState ", cs.RState)
	fmt.Println("Next", cs.Next)
	fmt.Println("Rnd", cs.Rnd)
	fmt.Println("Val", cs.Val)
}

func NewConsServer() *ConsServer {
	return &ConsServer{
		RState: &pb.State{make([]byte, 0), int32(0), uint32(0)},
		Next:   make(map[uint32]*pb.Blueprint, 0),
		Rnd:    make(map[uint32]uint32, 0),
		Val:    make(map[uint32]*pb.CV, 0),
		mu:     sync.RWMutex{},
	}
}

func NewConsServerWithCur(cur *pb.Blueprint, curc uint32) *ConsServer {
	return &ConsServer{
		Cur:    cur,
		CurC:   curc,
		RState: &pb.State{make([]byte, 0), int32(0), uint32(0)},
		Next:   make(map[uint32]*pb.Blueprint, 0),
		Rnd:    make(map[uint32]uint32, 0),
		Val:    make(map[uint32]*pb.CV, 0),
		mu:     sync.RWMutex{},
	}
}

func (cs *ConsServer) CSetState(ctx context.Context, nc *pb.CNewCur) (*pb.NewStateReply, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	//defer cs.PrintState("SetCur")
	if cs.RState.Compare(nc.State) == 1 {
		cs.RState = nc.State
	}
	
	if nc.CurC == 0 || nc.Cur.LearnedCompare(cs.Cur) == 1 {
		return &pb.NewStateReply{Cur: cs.Cur}, nil
	}
	
	var next *pb.Blueprint
	if n, ok := cs.Next[nc.CurC] ; ok {
		next = n
	}

	if nc.CurC == cs.CurC {
		if next != nil { return &pb.NewStateReply{Next: []*pb.Blueprint{next}}, nil }
		return &pb.NewStateReply{}, nil
	}

	

	if cs.Cur != nil && cs.Cur.Compare(nc.Cur) == 0 {
		return &pb.NewStateReply{}, errors.New("New Current Blueprint was uncomparable to previous.")
	}

	cs.Cur = nc.Cur
	cs.CurC = nc.CurC
	if next != nil { return &pb.NewStateReply{Next: []*pb.Blueprint{next}}, nil }
	return &pb.NewStateReply{}, nil
}

func (cs *ConsServer) CWriteN(ctx context.Context, rr *pb.DRead) (*pb.AdvReadReply, error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	//defer cs.PrintState("CReadS")

	if rr.Prop != nil {
		if n, ok := cs.Next[rr.CurC]; ok {
			if n != nil && !n.Equals(rr.Prop) {
				return nil, errors.New("Tried to overwrite Next.")
			}
		} else {
			cs.Next[rr.CurC] = rr.Prop
		}
	}

	var next []*pb.Blueprint
	if cs.Next[rr.CurC] != nil {
		next = []*pb.Blueprint{cs.Next[rr.CurC]}
	}
	if rr.CurC < cs.CurC {
		//Not sure if we should return an empty Next and State in this case.
		//Returning it is safer. The other faster.
		return &pb.AdvReadReply{State: cs.RState, Cur: cs.Cur, Next: next}, nil
	}

	return &pb.AdvReadReply{State: cs.RState, Next: next}, nil
}

func (cs *ConsServer) CReadS(ctx context.Context, rr *pb.Conf) (*pb.ReadReply, error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	//defer cs.PrintState("CReadS")

	if rr.This < cs.CurC {
		return &pb.ReadReply{Cur: &pb.ConfReply{cs.Cur, true}}, nil
	}
	var next []*pb.Blueprint
	if cs.Next[rr.This] != nil {
		next = []*pb.Blueprint{cs.Next[rr.This]}
	}
	if rr.Cur < cs.CurC {
		//Not sure if we should return an empty Next and State in this case.
		//Returning it is safer. The other faster.
		return &pb.ReadReply{State: cs.RState, Cur: &pb.ConfReply{cs.Cur, false}, Next: next}, nil
	}

	return &pb.ReadReply{State: cs.RState, Next: next}, nil
}

func (cs *ConsServer) CWriteS(ctx context.Context, wr *pb.WriteS) (*pb.WriteSReply, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	//defer cs.PrintState("CWriteS")
	if cs.RState.Compare(wr.State) == 1 {
		cs.RState = wr.State
	}

	if wr.Conf.This < cs.CurC {
		return &pb.WriteSReply{Cur: &pb.ConfReply{cs.Cur, true}}, nil
	}
	var next []*pb.Blueprint
	if cs.Next[wr.Conf.This] != nil {
		next = []*pb.Blueprint{cs.Next[wr.Conf.This]}
	}

	if wr.Conf.Cur < cs.CurC {
		return &pb.WriteSReply{Cur: &pb.ConfReply{cs.Cur, false}, Next: next}, nil
	}

	return &pb.WriteSReply{Next: next}, nil
}

func (cs *ConsServer) CPrepare(ctx context.Context, pre *pb.Prepare) (*pb.Promise, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	//defer cs.PrintState("CPrepare")

	var cur *pb.Blueprint
	if pre.CurC < cs.CurC {
		// Configuration outdated
		cur = cs.Cur
	}

	if cs.Next[pre.CurC] != nil {
		// Something was decided already
		return &pb.Promise{Cur: cur, Dec: cs.Next[pre.CurC]}, nil
	}

	if rnd, ok := cs.Rnd[pre.CurC]; !ok || pre.Rnd > rnd {
		// A Prepare in a new and higher round.
		cs.Rnd[pre.CurC] = pre.Rnd
		return &pb.Promise{Cur: cur, Val: cs.Val[pre.CurC]}, nil
	}

	return &pb.Promise{Cur: cur, Rnd: cs.Rnd[pre.CurC], Val: cs.Val[pre.CurC]}, nil
}

func (cs *ConsServer) CAccept(ctx context.Context, pro *pb.Propose) (lrn *pb.Learn, err error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	//defer cs.PrintState("Accept")

	var cur *pb.Blueprint
	if pro.CurC < cs.CurC {
		// Configuration outdated.
		cur = cs.Cur
	}

	if cs.Next[pro.CurC] != nil {
		// This instance is decided already
		return &pb.Learn{Cur: cur, Dec: cs.Next[pro.CurC]}, nil
	}

	if cs.Rnd[pro.CurC] > pro.Val.Rnd {
		// Accept in old round.
		return &pb.Learn{Cur: cur, Learned: false}, nil
	}

	cs.Rnd[pro.CurC] = pro.Val.Rnd
	cs.Val[pro.CurC] = pro.Val
	return &pb.Learn{Cur: cur, Learned: true}, nil
}
