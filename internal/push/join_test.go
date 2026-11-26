package push

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func joinServer(t *testing.T, status int, body string) (*httptest.Server, *map[string]string) {
	t.Helper()
	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got["path"] = r.Method + " " + r.URL.Path
		got["type"] = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		got["body"] = string(b)
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestJoinOK(t *testing.T) {
	srv, got := joinServer(t, http.StatusOK, `{"node_id":"n1","sampler_token":"tt_new","endpoint":"https://api.example"}`)
	e, err := Join(context.Background(), srv.URL, "tt_join_x", "web-1", "mid-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if e.SamplerToken != "tt_new" || e.NodeID != "n1" || e.Endpoint != "https://api.example" {
		t.Fatalf("enrollment = %+v", e)
	}
	if (*got)["path"] != "POST /v1/sampler/join" || (*got)["type"] != "application/json" {
		t.Fatalf("request = %v", *got)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte((*got)["body"]), &body); err != nil {
		t.Fatal(err)
	}
	if body["join_token"] != "tt_join_x" || body["display_name"] != "web-1" || body["machine_id"] != "mid-1" {
		t.Fatalf("body = %v", body)
	}
}

func TestJoinOmitsEmptyMachineID(t *testing.T) {
	srv, got := joinServer(t, http.StatusOK, `{"sampler_token":"t"}`)
	if _, err := Join(context.Background(), srv.URL, "j", "n", "", false); err != nil {
		t.Fatal(err)
	}
	if (*got)["body"] != `{"join_token":"j","display_name":"n"}` {
		t.Fatalf("body = %s", (*got)["body"])
	}
}

func TestJoin401(t *testing.T) {
	srv, _ := joinServer(t, http.StatusUnauthorized, `{"error":{"code":"unauthorized"}}`)
	if _, err := Join(context.Background(), srv.URL, "j", "n", "", false); !errors.Is(err, ErrJoinRejected) {
		t.Fatalf("err = %v", err)
	}
}

func TestJoin402(t *testing.T) {
	srv, _ := joinServer(t, http.StatusPaymentRequired, `{}`)
	if _, err := Join(context.Background(), srv.URL, "j", "n", "", false); !errors.Is(err, ErrPlanFull) {
		t.Fatalf("err = %v", err)
	}
}

func TestJoinOtherStatus(t *testing.T) {
	srv, _ := joinServer(t, http.StatusBadGateway, "bad gateway")
	_, err := Join(context.Background(), srv.URL, "j", "n", "", false)
	if err == nil || errors.Is(err, ErrJoinRejected) || errors.Is(err, ErrPlanFull) {
		t.Fatalf("err = %v", err)
	}
}

func TestJoinNoToken(t *testing.T) {
	srv, _ := joinServer(t, http.StatusOK, `{}`)
	if _, err := Join(context.Background(), srv.URL, "j", "n", "", false); err == nil {
		t.Fatal("want an error")
	}
}
