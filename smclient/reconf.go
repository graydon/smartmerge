package smclient

import (
	"errors"

	"github.com/golang/glog"
	conf "github.com/relab/smartMerge/confProvider"
	pb "github.com/relab/smartMerge/proto"
)

func (smc *SmClient) Reconf(cp conf.Provider, prop *pb.Blueprint) (cnt int, err error) {
	//Proposed blueprint is already in place, or outdated.
	if prop.Compare(smc.Blueps[0]) == 1 {
		glog.V(3).Infof("C%d: Proposal is already in place.", smc.Id)
		return 0, nil
	}

	_, cnt, err = smc.Doreconf(cp, prop, 0, nil)
	return
}

// Regular is: 0 for reconfiguration 1 for regular read, 2 for atomic read/write
func (smc *SmClient) Doreconf(cp conf.Provider, prop *pb.Blueprint, regular int, val []byte) (rst *pb.State, cnt int, err error) {
	if glog.V(6) {
		glog.Infof("C%d: Starting reconf\n", smc.Id)
	}

	cur := 0
	var wid []int // Did already write to these processes.
	var rid []int // Did already read from these processes.

forconfiguration:
	for i := 0; i < len(smc.Blueps); i++ {
		if i < cur {
			continue
		}

		if prop.Compare(smc.Blueps[i]) != 1 {
			// A new blueprint was proposed. Need to solve Lattice Agreement:
			var cr int
			prop, cnt, cr, err = smc.lagree(cp, i, prop)
			if err != nil {
				return nil, 0, err
			}
			if len(prop.Ids()) < MinSize {
				glog.Errorf("Aborting Reconfiguration to avoid unacceptable configuration.")
				return nil, cnt, errors.New("Abort before moving to unacceptable configuration.")
			}
			if cr > i {
				cur = cr
				continue
			}
		}

		if prop.LearnedCompare(smc.Blueps[i]) != -1 {
			if len(smc.Blueps) == i+1 && (cur == i || regular > 0) {
				// We are in the current configuration, do a read, to check for next configurations. No need to recontact.
				// If read or write operation: Need to read before writing.
				var st *pb.State
				var c int
				st, cur, c, err = smc.Doread(cp, cur, i, rid)
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
			}
			prop = smc.Blueps[len(smc.Blueps)-1]
			wid = nil // Empty rid on new Write Value.
		}

		if prop.LearnedCompare(smc.Blueps[i]) == -1 {
			// There exists a proposal => do WriteN

			cnf := cp.WriteC(smc.Blueps[i], wid)
			if cnf == nil {
				cnt++
			}

			writeN := new(pb.AWriteNReply)

			for j := 0; cnf != nil; j++ {
				writeN, err = cnf.AWriteN(&pb.WriteN{
					CurC: uint32(smc.Blueps[i].Len()),
					Next: prop,
				})
				cnt++

				if err != nil && j == 0 {
					glog.Errorf("C%d: error from OptimizedWriteN: %v\n", smc.Id, err)
					// Try again with full configuration.
					cnf = cp.FullC(smc.Blueps[i])
				}

				if err != nil && j == Retry {
					glog.Errorf("C%d: error %v from WriteN after %d retries: ", smc.Id, err, Retry)
					return nil, 0, err
				}

				if err == nil {
					break
				}
			}

			if i > 0 && glog.V(3) {
				glog.Infof("C%d: WriteN in Configuration with length %d\n ", smc.Id, smc.Blueps[i].Len())
			} else if glog.V(6) {
				glog.Infoln("WriteN returned.")
			}

			cur = smc.HandleNewCur(cur, writeN.Reply.GetCur())
			if rst.Compare(writeN.Reply.GetState()) == 1 {
				rst = writeN.Reply.GetState()
			}

			if c := writeN.Reply.GetCur(); c == nil || !c.Abort {
				wid = pb.Union(wid, writeN.MachineIDs)
				rid = pb.Union(rid, writeN.MachineIDs)
			}
		} else if i > cur || regular > 1 {

			rst = smc.WriteValue(&val, rst)

			cnf := cp.WriteC(smc.Blueps[i], nil)

			var setS *pb.SetStateReply

			for j := 0; ; j++ {
				setS, err = cnf.SetState(&pb.NewState{
					CurC:  uint32(smc.Blueps[i].Len()),
					State: rst})
				cnt++

				if err != nil && j == 0 {
					glog.Errorf("C%d: error from OptimizedSetState: %v\n", smc.Id, err)
					// Try again with full configuration.
					cnf = cp.FullC(smc.Blueps[i])
				}

				if err != nil && j == Retry {
					glog.Errorf("C%d: error %v from SetState after %d retries: ", smc.Id, err, Retry)
					return nil, 0, err
				}

				if err == nil {
					break
				}
			}

			if i > 0 && glog.V(3) {
				glog.Infof("C%d: Set State in Configuration with length %d\n ", smc.Id, smc.Blueps[i].Len())
			} else if glog.V(6) {
				glog.Infoln("Set state returned.")
			}

			cur = smc.HandleOneCur(i, setS.Reply.GetCur())
			smc.HandleNext(i, setS.Reply.GetNext())

		}
	}

	smc.SetNewCur(cur)
	if cnt > 2 {
		smc.SetCur(cp, smc.Blueps[0])
		cnt++
	}
	return rst, cnt, nil
}

func (smc *SmClient) lagree(cp conf.Provider, i int, prop *pb.Blueprint) (dec *pb.Blueprint, cnt, cur int, err error) {
	var rid []int
	prop = prop.Merge(smc.Blueps[i])
	for {
		cnf := cp.WriteC(smc.Blueps[i], rid)

		laProp := new(pb.LAPropReply)

		for j := 0; cnf != nil; j++ {
			laProp, err = cnf.LAProp(&pb.LAProposal{
				Conf: &pb.Conf{
					This: uint32(smc.Blueps[i].Len()),
					Cur:  uint32(smc.Blueps[i].Len())},
				Prop: prop})
			cnt++

			if err != nil && j == 0 {
				glog.Errorf("C%d: error from OptimizedLAProp: %v\n", smc.Id, err)
				// Try again with full configuration.
				cnf = cp.FullC(smc.Blueps[i])
			}

			if err != nil && j == Retry {
				glog.Errorf("C%d: error %v from LAProp after %d retries: ", smc.Id, err, Retry)
				return nil, 0, i, err
			}

			if err == nil {
				break
			}
		}

		if glog.V(4) {
			glog.Infof("C%d: LAProp returned.\n", smc.Id)
		}

		cur = smc.HandleNewCur(i, laProp.Reply.GetCur())
		la := laProp.Reply.GetLAState()
		if la != nil && !prop.LearnedEquals(la) {
			if glog.V(3) {
				glog.Infof("C%d: LAProp returned new state, try again.\n", smc.Id)
			}
			prop = la
			continue
		} else {
			break
		}

	}

	return prop, cnt, cur, nil
}

func (smc *SmClient) Doread(cp conf.Provider, curin, i int, rid []int) (st *pb.State, cur, cnt int, err error) {
	cnf := cp.ReadC(smc.Blueps[i], rid)
	if cnf == nil {
		cnt++
	}
	read := new(pb.AReadSReply)

	for j := 0; cnf != nil; j++ {
		read, err = cnf.AReadS(&pb.Conf{
			This: uint32(smc.Blueps[i].Len()),
			Cur:  uint32(smc.Blueps[i].Len()),
		})
		cnt++

		if err != nil && j == 0 {
			glog.Errorf("C%d: error from OptimizedReads: %v\n", smc.Id, err)
			// Try again with full configuration.
			cnf = cp.FullC(smc.Blueps[i])
		}

		if err != nil && j == Retry {
			glog.Errorf("C%d: error %v from ReadS after %d retries: ", smc.Id, err, Retry)
			return nil, 0, 0, err
		}

		if err == nil {
			break
		}
	}

	if glog.V(6) {
		glog.Infof("C%d: AReadS returned with replies from \n", smc.Id, read.MachineIDs)
	}
	cur = smc.HandleNewCur(curin, read.Reply.GetCur())

	return read.Reply.GetState(), cur, cnt, nil
}
