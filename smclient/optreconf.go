package smclient

import (
	"errors"

	"github.com/golang/glog"
	pb "github.com/relab/smartMerge/proto"
)

func (smc ConfigProvider) Reconf(prop *pb.Blueprint) (cnt int, err error) {
	//Proposed blueprint is already in place, or outdated.
	if prop.Compare(smc.getBluep(0)) == 1 {
		glog.V(3).Infof("C%d: Proposal is already in place.", smc.getId())
		return 0, nil
	}

	if smc.doCons {
		_, cnt, err = smc.consreconf(prop, true, nil)
	} else {
		_, cnt, err = smc.optreconf(prop, true, nil)
	}
	return
}

func (smc ConfigProvider) optreconf(prop *pb.Blueprint, regular bool, val []byte) (rst *pb.State, cnt int, err error) {
	if glog.V(6) {
		glog.Infof("C%d: Starting reconf\n", smc.getId())
	}

	if prop.Compare(smc.getBluep(0)) != 1 {
		// A new blueprint was proposed. Need to solve Lattice Agreement:
		prop, cnt, err = smc.lagree(prop)
		if err != nil {
			return nil, 0, err
		}
		if len(prop.Ids()) < MinSize {
			glog.Errorf("Aborting Reconfiguration to avoid unacceptable configuration.")
			return nil, cnt, errors.New("Abort before moving to unacceptable configuration.")
		}
	}

	cur := 0
	las := new(pb.Blueprint)
	var rid []uint32 //Oups: Should we have two, one for doread, one for writing?

forconfiguration:
	for i := 0; i < smc.getNBlueps(); i++ {
		if i < cur {
			continue
		}

		if prop.LearnedCompare(smc.getBluep(i)) != -1 {
			if len(smc.Blueps) > i+1 {
				prop = smc.getBluep(smc.getNBlueps() - 1)
				rid = nil // Empty rid on new Write Value.
			} else if cur == i || !regular {
				// We are in the current configuration, do a read, to check for next configurations. No need to recontact.
				// If atomic: Need to read before writing.
				var st *pb.State
				var c int
				st, cur, c, err = smc.doread(cur, i, rid)
				if err != nil {
					return nil, 0, err
				}
				cnt += c
				if rst.Compare(st) == 1 {
					rst = st
				}

				if i < cur {
					continue forconfiguration
				}

				prop = smc.getBluep(smc.getNBlueps() - 1)
				rid = nil // Empty rid on new Write Value.
			}
		}

		if prop.LearnedCompare(smc.getBluep(i)) == -1 {
			// There exists a proposal => do WriteN

			cnf := smc.getWriteC(i, rid)

			writeN := new(pb.AWriteNReply)

			for j := 0; cnf != nil; j++ {
				writeN, err = cnf.AWriteN(&pb.WriteN{
					CurC: uint32(smc.getLenBluep(i)),
					Next: prop,
				})
				cnt++

				if err != nil && j == 0 {
					glog.Errorf("C%d: error from OptimizedWriteN: %v\n", smc.getId(), err)
					// Try again with full configuration.
					cnf = smc.getFullC(i)
				}

				if err != nil && j == Retry {
					glog.Errorf("C%d: error %v from WriteN after %d retries: ", smc.getId(), err, Retry)
					return nil, 0, err
				}

				if err == nil {
					break
				}
			}

			cur = smc.handleNewCur(cur, writeN.Reply.GetCur())
			las = las.Merge(writeN.Reply.GetLAState())
			if rst.Compare(writeN.Reply.GetState()) == 1 {
				rst = writeN.Reply.GetState()
			}

			if c := writeN.Reply.GetCur(); c == nil || !c.Abort {
				rid = pb.Union(rid, writeN.MachineIDs)
			}
		} else if i > cur || !regular {

			rst = smc.WriteValue(val, rst)

			cnf := smc.getWriteC(i, nil)

			var setS *pb.SetStateReply

			for j := 0; ; j++ {
				setS, err = cnf.SetState(&pb.NewState{
					CurC:    uint32(smc.getLenBluep(i)),
					Cur:     smc.getBluep(i),
					State:   rst,
					LAState: las})
				cnt++

				if err != nil && j == 0 {
					glog.Errorf("C%d: error from OptimizedSetState: %v\n", smc.getId(), err)
					// Try again with full configuration.
					cnf = smc.getFullC(i)
				}

				if err != nil && j == Retry {
					glog.Errorf("C%d: error %v from SetState after %d retries: ", smc.getId(), err, Retry)
					return nil, 0, err
				}

				if err == nil {
					break
				}
			}

			if i > 0 && glog.V(3) {
				glog.Infof("C%d: Set State in Configuration with length %d\n ", smc.getId(), smc.Blueps[i].Len())
			} else if glog.V(6) {
				glog.Infoln("Set state returned.")
			}

			cur = smc.handleOneCur(i, setS.Reply.GetCur())
			smc.handleNext(i, setS.Reply.GetNext())
		}
	}

	smc.setNewCur(cur)
	return rst, cnt, nil
}

func (smc ConfigProvider) lagree(prop *pb.Blueprint) (dec *pb.Blueprint, cnt int, err error) {
	cur := 0
	var rid []uint32
	prop = prop.Merge(smc.getBluep(0))
	for i := 0; i < smc.getNBlueps(); i++ {
		if i < cur {
			continue
		}

		cnf := smc.getWriteC(i, rid)

		laProp := new(pb.LAPropReply)

		for j := 0; cnf != nil; j++ {
			laProp, err = cnf.LAProp(&pb.LAProposal{
				Conf: &pb.Conf{
					This: uint32(smc.getLenBluep(i)),
					Cur:  uint32(smc.getLenBluep(cur))},
				Prop: prop})
			cnt++

			if err != nil && j == 0 {
				glog.Errorf("C%d: error from OptimizedLAProp: %v\n", smc.getId(), err)
				// Try again with full configuration.
				cnf = smc.getFullC(i)
			}

			if err != nil && j == Retry {
				glog.Errorf("C%d: error %v from LAProp after %d retries: ", smc.getId(), err, Retry)
				return nil, 0, err
			}

			if err == nil {
				break
			}
		}

		if glog.V(4) {
			glog.Infof("C%d: LAProp returned.\n", smc.getId())
		}

		cur = smc.handleNewCur(cur, laProp.Reply.GetCur(), false)
		la := laProp.Reply.GetLAState()
		if la != nil && !prop.LearnedEquals(la) {
			if glog.V(3) {
				glog.Infof("C%d: LAProp returned new state, try again.\n", smc.getId())
			}
			prop = la
			i--
			rid = nil
			continue
		}

		if smc.getNBlueps() > i+1 {
			if c := laProp.Reply.GetCur(); c == nil || !c.Abort {
				rid = pb.Union(rid, laProp.MachineIDs)
			}
		}
	}

	smc.setNewCur(cur)
	return prop, cnt, nil
}

func (smc ConfigProvider) doread(curin, i int, rid []uint32) (st *pb.State, cur, cnt int, err error) {
	cnf := smc.getReadC(i, rid)

	read := new(pb.AReadSReply)

	for j := 0; cnf != nil; j++ {
		read, err = cnf.AReadS(&pb.Conf{
			This: uint32(smc.getLenBluep(i)),
			Cur:  uint32(smc.getLenBluep(i)),
		})
		cnt++

		if err != nil && j == 0 {
			glog.Errorf("C%d: error from OptimizedReads: %v\n", smc.getId(), err)
			// Try again with full configuration.
			cnf = smc.getFullC(i)
		}

		if err != nil && j == Retry {
			glog.Errorf("C%d: error %v from ReadS after %d retries: ", smc.getId(), err, Retry)
			return nil, 0, 0, err
		}

		if err == nil {
			break
		}
	}

	if glog.V(6) {
		glog.Infof("C%d: AReadS returned with replies from \n", smc.getId(), read.MachineIDs)
	}
	cur = smc.handleNewCur(curin, read.Reply.GetCur())

	return read.Reply.GetState(), cur, cnt, nil
}

func (smc ConfigProvider) getWriteValue(val []byte, st *pb.State) *pb.State {
	if val == nil {
		return st
	}
	st = &pb.State{Value: val, Timestamp: st.Timestamp + 1, Writer: smc.getId()}
	val = nil
	return st
}
