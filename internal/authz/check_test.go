package authz

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"
)

type fakeClient struct{ allow bool }
func (f *fakeClient) Check(ctx context.Context, user, object, relation string) (bool, error) {
    return f.allow, nil
}

func TestCanAllowed(t *testing.T) {
    c := &fakeClient{allow: true}
    r := httptest.NewRequest(http.MethodGet, "/", nil)
    r.Header.Set("X-Principal", "user:alice")
    allowed, err := Can(context.Background(), c, r, "order:ord123", "can_refund")
    if err != nil { t.Fatalf("unexpected err: %v", err) }
    if !allowed { t.Fatalf("expected allowed=true") }
}

func TestCanDenied(t *testing.T) {
    c := &fakeClient{allow: false}
    r := httptest.NewRequest(http.MethodGet, "/", nil)
    r.Header.Set("X-Principal", "user:charlie")
    allowed, err := Can(context.Background(), c, r, "order:ord123", "can_refund")
    if err != nil { t.Fatalf("unexpected err: %v", err) }
    if allowed { t.Fatalf("expected allowed=false") }
}

