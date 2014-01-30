package registry

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/coreos/go-etcd/etcd"
	log "github.com/golang/glog"

	"github.com/coreos/coreinit/event"
	"github.com/coreos/coreinit/job"
	"github.com/coreos/coreinit/machine"
)

const (
	jobPrefix     = "/job/"
	payloadPrefix = "/payload/"
)

func (r *Registry) GetAllPayloads() []job.JobPayload {
	var payloads []job.JobPayload

	key := path.Join(keyPrefix, payloadPrefix)
	resp, err := r.etcd.Get(key, true, true)

	if err != nil {
		log.Errorf(err.Error())
		return payloads
	}

	for _, node := range resp.Node.Nodes {
		var jp job.JobPayload
		//TODO: Handle the error generated by unmarshal
		unmarshal(node.Value, &jp)

		if err != nil {
			log.Errorf(err.Error())
			continue
		}

		payloads = append(payloads, jp)
	}

	return payloads
}

// List the jobs all Machines are scheduled to run
func (r *Registry) GetAllJobs() []job.Job {
	var jobs []job.Job

	key := path.Join(keyPrefix, jobPrefix)
	resp, err := r.etcd.Get(key, true, true)

	if err != nil {
		log.Errorf(err.Error())
		return jobs
	}

	for _, node := range resp.Node.Nodes {
		if j := r.GetJob(path.Base(node.Key)); j != nil {
			jobs = append(jobs, *j)
		}
	}

	return jobs
}

func (r *Registry) GetAllJobsByMachine(match *machine.Machine) []job.Job {
	var jobs []job.Job

	key := path.Join(keyPrefix, jobPrefix)
	resp, err := r.etcd.Get(key, true, true)

	if err != nil {
		log.Errorf(err.Error())
		return jobs
	}

	for _, node := range resp.Node.Nodes {
		if j := r.GetJob(path.Base(node.Key)); j != nil {
			tgt := r.GetJobTarget(j.Name)
			if tgt != nil && tgt.BootId == match.BootId {
				jobs = append(jobs, *j)
			}
		}
	}

	return jobs
}

func (r *Registry) GetJobTarget(jobName string) *machine.Machine {
	// Figure out to which Machine this Job is scheduled
	key := path.Join(keyPrefix, jobPrefix, jobName, "target")
	resp, err := r.etcd.Get(key, false, true)
	if err != nil {
		return nil
	}

	return machine.New(resp.Node.Value, "", make(map[string]string, 0))
}

func (r *Registry) GetJob(jobName string) *job.Job {
	key := path.Join(keyPrefix, jobPrefix, jobName, "object")
	resp, err := r.etcd.Get(key, false, true)

	// Assume the error was KeyNotFound and return an empty data structure
	if err != nil {
		return nil
	}

	var j job.Job
	//TODO: Handle the error generated by unmarshal
	unmarshal(resp.Node.Value, &j)

	return &j
}

func (r *Registry) CreatePayload(jp *job.JobPayload) {
	key := path.Join(keyPrefix, payloadPrefix, jp.Name)
	json, _ := marshal(jp)
	r.etcd.Set(key, json, 0)
}

func (r *Registry) GetPayload(payloadName string) *job.JobPayload {
	key := path.Join(keyPrefix, payloadPrefix, payloadName)
	resp, err := r.etcd.Get(key, false, true)

	// Assume the error was KeyNotFound and return an empty data structure
	if err != nil {
		return nil
	}

	var jp job.JobPayload
	//TODO: Handle the error generated by unmarshal
	unmarshal(resp.Node.Value, &jp)

	return &jp
}

func (r *Registry) DestroyPayload(payloadName string) {
	key := path.Join(keyPrefix, payloadPrefix, payloadName)
	r.etcd.Delete(key, false)
}

func (r *Registry) CreateJob(j *job.Job) {
	key := path.Join(keyPrefix, jobPrefix, j.Name, "object")
	json, _ := marshal(j)
	r.etcd.Set(key, json, 0)
}

func (r *Registry) ScheduleJob(jobName string, machName string) {
	key := path.Join(keyPrefix, jobPrefix, jobName, "target")
	r.etcd.Set(key, machName, 0)
}

func (r *Registry) StopJob(jobName string) {
	key := path.Join(keyPrefix, jobPrefix, jobName, "target")
	r.etcd.Delete(key, true)
}

func (r *Registry) ClaimJob(jobName string, m *machine.Machine, ttl time.Duration) bool {
	return r.acquireLeadership(fmt.Sprintf("job-%s", jobName), m.BootId, ttl)
}

func filterEventJobCreated(resp *etcd.Response) *event.Event {
	if resp.Action != "set" {
		return nil
	}

	baseName := path.Base(resp.Node.Key)
	if baseName != "object" {
		return nil
	}

	var j job.Job
	err := unmarshal(resp.Node.Value, &j)
	if err != nil {
		log.V(1).Infof("Failed to deserialize Job: %s", err)
		return nil
	}

	return &event.Event{"EventJobCreated", j, nil}
}

func filterEventJobScheduled(resp *etcd.Response) *event.Event {
	if resp.Action != "set" {
		return nil
	}

	dir, baseName := path.Split(resp.Node.Key)
	if baseName != "target" {
		return nil
	}

	mach := machine.New(resp.Node.Value, "", make(map[string]string, 0))
	jobName := path.Base(strings.TrimSuffix(dir, "/"))

	return &event.Event{"EventJobScheduled", jobName, mach}
}

func filterEventJobStopped(resp *etcd.Response) *event.Event {
	if resp.Action != "delete" && resp.Action != "expire" {
		return nil
	}

	dir, baseName := path.Split(resp.Node.Key)
	if baseName != "target" {
		return nil
	}

	dir = strings.TrimSuffix(dir, "/")
	dir, jobName := path.Split(dir)

	return &event.Event{"EventJobStopped", jobName, nil}
}
