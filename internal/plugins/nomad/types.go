// Package nomad provides a minimal client for the HashiCorp Nomad API.
package nomad

// Job is a summary of a Nomad job returned by the /v1/jobs endpoint.
type Job struct {
	ID        string
	Name      string
	Namespace string
	Type      string // "service", "batch", "system", "sysbatch"
	Status    string // "pending", "running", "dead"
	Allocs    AllocCounts
}

// AllocCounts holds per-state allocation counts for a job, summed across task groups.
type AllocCounts struct {
	Running  int
	Queued   int // "Queued" in Nomad parlance (pending scheduling)
	Starting int
	Failed   int
	Complete int
	Lost     int
	Unknown  int
}

// Total returns the sum of all allocation counts.
func (a AllocCounts) Total() int {
	return a.Running + a.Queued + a.Starting + a.Failed + a.Complete + a.Lost + a.Unknown
}

// Desired returns the number of desired running allocations
// (running + queued + starting = what Nomad is trying to have running).
func (a AllocCounts) Desired() int {
	return a.Running + a.Queued + a.Starting
}

// jobListStub matches the JSON shape returned by GET /v1/jobs.
type jobListStub struct {
	ID         string      `json:"ID"`
	Name       string      `json:"Name"`
	Namespace  string      `json:"Namespace"`
	Type       string      `json:"Type"`
	Status     string      `json:"Status"`
	JobSummary *jobSummary `json:"JobSummary"`
}

type jobSummary struct {
	Summary map[string]taskGroupSummary `json:"Summary"`
}

type taskGroupSummary struct {
	Queued   int `json:"Queued"`
	Complete int `json:"Complete"`
	Failed   int `json:"Failed"`
	Running  int `json:"Running"`
	Starting int `json:"Starting"`
	Lost     int `json:"Lost"`
	Unknown  int `json:"Unknown"`
}
