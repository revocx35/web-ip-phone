package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// recOut records the control messages the service sends to a client.
type recOut struct {
	mu   sync.Mutex
	msgs []string
}

func (o *recOut) SendJSON(v any) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.msgs = append(o.msgs, fmt.Sprintf("%+v", v))
	return true
}
func (o *recOut) SendBinary([]byte) bool { return true }
func (o *recOut) Close(string)           {}

func (o *recOut) count(sub string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, m := range o.msgs {
		if m == "{Type:"+sub+"}" {
			n++
		}
	}
	return n
}

func TestContactsAPI(t *testing.T) {
	e := newEnv(t, "")
	admin := e.setupAdmin()
	e.do(req{method: "POST", path: "/api/v1/admin/users", cookie: admin, body: map[string]any{"username": "carol", "password": "Car0l-Secret-Pass"}})
	e.do(req{method: "POST", path: "/api/v1/admin/users", cookie: admin, body: map[string]any{"username": "dave", "password": "Dav3-Secret-Pass"}})
	carol, _ := e.login("carol", "Car0l-Secret-Pass", true) // app token: no CSRF header needed
	dave, _ := e.login("dave", "Dav3-Secret-Pass", false)

	// live updates: carol's and dave's open connections
	ctx := context.Background()
	carolWS, daveWS := &recOut{}, &recOut{}
	e.s.svc.Connect(ctx, 2, "carol", 0, nil, carolWS)
	e.s.svc.Connect(ctx, 3, "dave", 0, nil, daveWS)

	list := func(who string, bearer bool) []any {
		r := req{method: "GET", path: "/api/v1/contacts"}
		if bearer {
			r.bearer = who
		} else {
			r.cookie = who
		}
		w, m := e.do(r)
		if w.Code != 200 {
			t.Fatalf("list: %d %v", w.Code, m)
		}
		return m["contacts"].([]any)
	}

	// create: separators are stripped from phone numbers, not from SIP names
	w, m := e.do(req{method: "POST", path: "/api/v1/contacts", bearer: carol, body: map[string]any{
		"name": "  Alice Example ", "favorite": true,
		"numbers": []map[string]string{{"label": "Mobile", "number": "+49 (151) 123-45.67"}, {"number": "john.doe-2"}},
	}})
	if w.Code != 201 {
		t.Fatalf("create: %d %v", w.Code, m)
	}
	nums := m["numbers"].([]any)
	if m["name"] != "Alice Example" || nums[0].(map[string]any)["number"] != "+491511234567" ||
		nums[1].(map[string]any)["number"] != "john.doe-2" || m["favorite"] != true || m["shared"] != false || m["editable"] != true {
		t.Fatalf("created: %v", m)
	}
	id := int(m["id"].(float64))
	if carolWS.count("contacts") != 1 || daveWS.count("contacts") != 0 {
		t.Fatalf("live update: carol %d, dave %d", carolWS.count("contacts"), daveWS.count("contacts"))
	}
	if len(list(dave, false)) != 0 {
		t.Fatal("dave sees carol's contact")
	}

	// validation: header injection, empty, too many numbers, unknown fields
	bad := []map[string]any{
		{"name": "x", "numbers": []map[string]string{{"number": "100\r\nVia: evil"}}},
		{"name": "x", "numbers": []map[string]string{{"number": "100<sip:a@b>"}}},
		{"name": " ", "numbers": []map[string]string{{"number": "100"}}},
		{"name": "x", "numbers": []map[string]string{}},
		{"name": "x\x00", "numbers": []map[string]string{{"number": "100"}}},
		{"name": "x", "numbers": []map[string]string{{"number": "100", "label": "0123456789012345678901234567890123"}}},
		{"name": "x", "numbers": []map[string]string{{"number": "100"}}, "ownerId": 1},
	}
	eleven := []map[string]string{}
	for i := 0; i < 11; i++ {
		eleven = append(eleven, map[string]string{"number": "10" + fmt.Sprint(i)})
	}
	bad = append(bad, map[string]any{"name": "x", "numbers": eleven})
	for i, b := range bad {
		if w, _ := e.do(req{method: "POST", path: "/api/v1/contacts", bearer: carol, body: b}); w.Code != 400 {
			t.Errorf("bad contact %d accepted: %d", i, w.Code)
		}
	}

	// only admins create shared contacts
	shared := map[string]any{"name": "Reception", "shared": true, "numbers": []map[string]string{{"number": "2001"}}}
	if w, _ := e.do(req{method: "POST", path: "/api/v1/contacts", bearer: carol, body: shared}); w.Code != 403 {
		t.Fatalf("user created a shared contact: %d", w.Code)
	}
	w, m = e.do(req{method: "POST", path: "/api/v1/contacts", cookie: admin, body: shared})
	if w.Code != 201 || m["shared"] != true {
		t.Fatalf("shared: %d %v", w.Code, m)
	}
	sid := int(m["id"].(float64))
	if daveWS.count("contacts") != 1 || carolWS.count("contacts") != 2 {
		t.Fatal("shared change not pushed to everyone")
	}
	if l := list(dave, false); len(l) != 1 || l[0].(map[string]any)["editable"] != false {
		t.Fatalf("dave's view of the shared phone book: %v", l)
	}
	// users cannot change or delete it, but may star it for themselves
	sp := fmt.Sprintf("/api/v1/contacts/%d", sid)
	if w, _ := e.do(req{method: "PUT", path: sp, cookie: dave, body: shared}); w.Code != 403 {
		t.Fatalf("user changed a shared contact: %d", w.Code)
	}
	if w, _ := e.do(req{method: "DELETE", path: sp, cookie: dave}); w.Code != 403 {
		t.Fatalf("user deleted a shared contact: %d", w.Code)
	}
	if w, m := e.do(req{method: "PUT", path: sp + "/favorite", cookie: dave, body: map[string]bool{"favorite": true}}); w.Code != 200 || m["favorite"] != true {
		t.Fatalf("favorite: %d %v", w.Code, m)
	}
	if l := list(carol, true); l[0].(map[string]any)["name"] != "Alice Example" || l[1].(map[string]any)["favorite"] != false {
		t.Fatalf("dave's favorite leaked to carol: %v", l)
	}

	// other users' contacts are invisible: 404, not 403
	cp := fmt.Sprintf("/api/v1/contacts/%d", id)
	for _, r := range []req{
		{method: "PUT", path: cp, cookie: dave, body: map[string]any{"name": "x", "numbers": []map[string]string{{"number": "1"}}}},
		{method: "DELETE", path: cp, cookie: dave},
		{method: "PUT", path: cp + "/favorite", cookie: dave, body: map[string]bool{"favorite": true}},
		{method: "PUT", path: cp, cookie: admin, body: map[string]any{"name": "x", "numbers": []map[string]string{{"number": "1"}}}},
	} {
		if w, _ := e.do(r); w.Code != 404 {
			t.Errorf("%s %s by another user: %d", r.method, r.path, w.Code)
		}
	}

	// update keeps the favorite unless given; a user cannot share their contact
	upd := map[string]any{"name": "Alice", "numbers": []map[string]string{{"label": "Office", "number": "1005"}}}
	if w, m := e.do(req{method: "PUT", path: cp, bearer: carol, body: upd}); w.Code != 200 || m["name"] != "Alice" || m["favorite"] != true ||
		len(m["numbers"].([]any)) != 1 {
		t.Fatalf("update: %d %v", w.Code, m)
	}
	upd["shared"] = true
	if w, _ := e.do(req{method: "PUT", path: cp, bearer: carol, body: upd}); w.Code != 403 {
		t.Fatalf("user shared a contact: %d", w.Code)
	}

	// cookie sessions need the CSRF header
	if w, _ := e.do(req{method: "POST", path: "/api/v1/contacts", cookie: dave, body: upd, headers: map[string]string{"X-Requested-With": ""}}); w.Code != 403 {
		t.Fatalf("contact created without CSRF header: %d", w.Code)
	}

	// shared changes are audited
	e.do(req{method: "DELETE", path: sp, cookie: admin})
	w, _ = e.do(req{method: "GET", path: "/api/v1/admin/audit", cookie: admin})
	var au []struct{ Action string }
	json.Unmarshal(w.Body.Bytes(), &au)
	actions := map[string]bool{}
	for _, a := range au {
		actions[a.Action] = true
	}
	if !actions["contact.create"] || !actions["contact.delete"] {
		t.Fatalf("audit: %v", actions)
	}
	if w, _ := e.do(req{method: "DELETE", path: cp, bearer: carol}); w.Code != 200 || len(list(carol, true)) != 0 {
		t.Fatal("delete own contact")
	}
}
