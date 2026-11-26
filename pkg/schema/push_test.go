package schema

import (
	"encoding/json"
	"testing"
)

func TestPushProtocolJSON(t *testing.T) {
	zero := 0.0
	yes := true
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"join", JoinRequest{JoinToken: "j", DisplayName: "n"}, `{"join_token":"j","display_name":"n"}`},
		{"join machine", JoinRequest{JoinToken: "j", DisplayName: "n", MachineID: "m"}, `{"join_token":"j","display_name":"n","machine_id":"m"}`},
		{"enroll", EnrollResponse{"n1", "t", "https://e"}, `{"node_id":"n1","sampler_token":"t","endpoint":"https://e"}`},
		{"ingest empty", IngestResponse{}, `{"accepted":0,"rejected":0}`},
		{"ingest full", IngestResponse{Accepted: 2, Rejected: 1, Error: "x", Interval: "5s", Update: "1.5.0"}, `{"accepted":2,"rejected":1,"error":"x","interval":"5s","update":"1.5.0"}`},
		{"ingest proc_cwd", IngestResponse{ProcCwd: &yes}, `{"accepted":0,"rejected":0,"proc_cwd":true}`},
		{"plan full", IngestPlanFull{60}, `{"retry_after":60}`},
		{"gone", IngestGone{"exit", GoneHostRemoved}, `{"action":"exit","reason":"host_removed"}`},
		{"control", ControlNextResponse{"m1", ControlKindKillProcess, json.RawMessage(`{"pid":1}`)}, `{"id":"m1","kind":"kill_process","payload":{"pid":1}}`},
		{"kill", KillProcessPayload{42}, `{"pid":42}`},
		{"scan default", DiskScanPayload{JobID: "j1"}, `{"job_id":"j1","root":""}`},
		{"scan all", DiskScanPayload{JobID: "j1", MaxDepth: 2, MinBytes: 1000, MinFrac: &zero}, `{"job_id":"j1","root":"","max_depth":2,"min_bytes":1000,"min_frac":0}`},
		{"progress", DiskScanProgressResponse{true}, `{"cancel":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.v)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}
