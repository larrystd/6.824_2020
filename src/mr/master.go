package mr

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sync"
	"time"
)

type JobState int

const (
	Mapping JobState = iota
	Reducing
	Done
)

type Master struct {
	State       JobState
	NReduce     int
	MapTasks    []*MapTask
	ReduceTasks []*ReduceTask

	MappedTaskId map[int]struct{}
	MaxTaskId    int
	Mutex        sync.Mutex

	WorkerCount  int
	ExcitedCount int
}

// Your code here -- RPC handlers for the worker to call.

const TIMEOUT = 10 * time.Second

// work通过rpc调用之, reply是指针, 被赋值
func (m *Master) RequestTask(_ *Placeholder, reply *Task) error {
	reply.Operation = ToWait

	if m.State == Mapping { // master的状态
		for _, task := range m.MapTasks { // 从m.MapTasks中分配task到worker
			now := time.Now()
			m.Mutex.Lock()
			if task.State == Executing && task.StartTime.Add(TIMEOUT).Before(now) {
				task.State = Pending
			}
			if task.State == Pending {
				task.StartTime = now // 设置task的一些信息
				task.State = Executing
				m.MaxTaskId++
				task.Id = m.MaxTaskId
				m.Mutex.Unlock()
				log.Printf("assigned map task %d %s", task.Id, task.Filename)

				reply.Operation = ToRun
				reply.IsMap = true
				reply.NReduce = m.NReduce
				reply.Map = *task
				return nil
			}
			m.Mutex.Unlock()
		}

	} else if m.State == Reducing {
		for _, task := range m.ReduceTasks {
			now := time.Now()
			m.Mutex.Lock()
			if task.State == Executing && task.StartTime.Add(TIMEOUT).Before(now) {
				task.State = Pending
			}
			if task.State == Pending {
				task.StartTime = now
				task.State = Executing
				task.IntermediateFilenames = nil
				for id := range m.MappedTaskId {
					task.IntermediateFilenames = append(task.IntermediateFilenames, fmt.Sprintf("mr-%d-%d", id, task.Id))
				}
				m.Mutex.Unlock()
				log.Printf("assigned reduce task %d", task.Id)

				reply.Operation = ToRun
				reply.IsMap = false
				reply.NReduce = m.NReduce
				reply.Reduce = *task
				return nil
			}
			m.Mutex.Unlock()
		}
	}
	return nil
}

// 终止阶段
func (m *Master) Finish(args *FinishArgs, _ *Placeholder) error {
	if args.IsMap {
		for _, task := range m.MapTasks {
			if task.Id == args.Id {
				task.State = Finished
				log.Printf("finished task %d, total %d", task.Id, len(m.MapTasks))
				m.MappedTaskId[task.Id] = struct{}{}
				break
			}
		}
		//
		for _, t := range m.MapTasks {
			if t.State != Finished {
				return nil
			}
		}
		m.State = Reducing
	} else {
		for _, task := range m.ReduceTasks {
			if task.Id == args.Id {
				task.State = Finished
				break
			}
		}
		//
		for _, t := range m.ReduceTasks {
			if t.State != Finished {
				return nil
			}
		}
		m.State = Done
	}
	return nil
}

//
// start a thread that listens for RPCs from worker.go
// 执行
func (m *Master) server() {
	rpc.Register(m) // 注册Master* m, worker可通过rpc调用master的成员函数
	rpc.HandleHTTP()
	// l, e := net.Listen("tcp", ":1234")
	sockname := masterSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

//
// main/mrmaster.go calls Done() periodically to find out
// if the entire job has finished.
//
func (m *Master) Done() bool {
	return m.State == Done
}

//
// create a Master.
// main/mrmaster.go calls this function.
// nReduce is the number of reduce tasks to use.
//

func MakeMaster(files []string, nReduce int) *Master {
	m := Master{
		NReduce:      nReduce,
		MaxTaskId:    0,
		MappedTaskId: make(map[int]struct{}),
	}

	for _, f := range files { // files 有许多个, 添加到MapTask和ReduceTask列表中, 以分发给worker
		m.MapTasks = append(m.MapTasks, &MapTask{TaskMeta: TaskMeta{State: Pending}, Filename: f})
	}
	for i := 0; i < nReduce; i++ {
		m.ReduceTasks = append(m.ReduceTasks, &ReduceTask{TaskMeta: TaskMeta{State: Pending, Id: i}})
	}
	m.State = Mapping

	m.server()
	return &m
}
