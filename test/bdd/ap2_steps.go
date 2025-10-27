package bdd

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/http/httptest"
    "os"
    "strings"

    ap2handlers "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/ap2"
    postgresdb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
    "github.com/cucumber/godog"
)

func (w *PipelineWorld) registerAP2Steps(sc *godog.ScenarioContext) {
    sc.Step(`^AP2 test servers are running$`, w.ap2StartServers)
    sc.Step(`^I create an AP2 intent for customer "([^"]+)" with cart "([^"]+)"$`, w.ap2CreateIntent)
    sc.Step(`^I authorize the AP2 intent$`, w.ap2AuthorizeIntent)
    sc.Step(`^I execute the AP2 intent$`, w.ap2ExecuteIntent)
    sc.Step(`^the AP2 execute response contains an order and invoice link$`, w.ap2AssertExecuteResponse)
    sc.Step(`^AP2 status for the execution is available$`, w.ap2AssertStatus)
    sc.Step(`^the cart for customer "([^"]+)" is empty$`, w.ap2AssertCartEmpty)
}

// ap2StartServers starts a stub Restate runtime (cart + checkout) and an AP2 HTTP server that
// mounts the real AP2 handlers under /ap2/*.
func (w *PipelineWorld) ap2StartServers() error {
    w.ensureDatabase()

    // Stub Restate runtime endpoints used by AP2 handlers
    runtimeMux := http.NewServeMux()
    runtimeMux.HandleFunc("/cart.sv1.CartService/", func(wr http.ResponseWriter, r *http.Request) {
        // Path: /cart.sv1.CartService/{customer}/{Method}
        parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/cart.sv1.CartService/"), "/")
        if len(parts) < 2 || r.Method != http.MethodPost {
            http.Error(wr, "not found", http.StatusNotFound)
            return
        }
        customerID := parts[0]
        method := parts[1]

        b, _ := io.ReadAll(r.Body)
        _ = r.Body.Close()
        w.debugf("[AP2-Test] Runtime Cart %s %s body=%s", method, r.URL.Path, string(b))

        // Seed a default non-empty cart if missing
        seedIfMissing := func() {
            if _, ok := w.ap2RuntimeCarts[customerID]; !ok {
                w.ap2RuntimeCarts[customerID] = ap2CartState{
                    MerchantID:  "merchant-001",
                    Items:       []map[string]any{{"product_id": "sku-1", "name": "Widget", "quantity": 1.0, "unit_price": 49.99}},
                    TotalAmount: 49.99,
                }
            }
        }

        switch method {
        case "ViewCart":
            seedIfMissing()
            st := w.ap2RuntimeCarts[customerID]
            resp := map[string]any{
                "cart_state": map[string]any{
                    "merchant_id":  st.MerchantID,
                    "items":        st.Items,
                    "total_amount": st.TotalAmount,
                },
            }
            wr.Header().Set("Content-Type", "application/json")
            _ = json.NewEncoder(wr).Encode(resp)
            return
        case "ClearCart":
            // Clear in-memory state
            w.ap2RuntimeCarts[customerID] = ap2CartState{MerchantID: "", Items: []map[string]any{}, TotalAmount: 0}
            resp := map[string]any{"success": true, "message": "Cart cleared successfully"}
            wr.Header().Set("Content-Type", "application/json")
            _ = json.NewEncoder(wr).Encode(resp)
            return
        default:
            http.Error(wr, "not found", http.StatusNotFound)
            return
        }
    })
    runtimeMux.HandleFunc("/order.sv1.OrderService/", func(wr http.ResponseWriter, r *http.Request) {
        // Path: /order.sv1.OrderService/{orderId}/Checkout
        if !strings.HasSuffix(r.URL.Path, "/Checkout") || r.Method != http.MethodPost {
            http.Error(wr, "not found", http.StatusNotFound)
            return
        }
        parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/order.sv1.OrderService/"), "/")
        orderID := "test-order"
        if len(parts) >= 2 && parts[1] == "Checkout" {
            orderID = parts[0]
        }
        b, _ := io.ReadAll(r.Body)
        _ = r.Body.Close()
        w.debugf("[AP2-Test] Runtime Checkout %s order=%s body=%s", r.URL.Path, orderID, string(b))

        // Parse customer_id from request to clear their cart after checkout
        var j map[string]any
        _ = json.Unmarshal(b, &j)
        if cid, _ := j["customer_id"].(string); cid != "" {
            // emulate cart clearing side effect of checkout
            w.ap2RuntimeCarts[cid] = ap2CartState{MerchantID: "", Items: []map[string]any{}, TotalAmount: 0}
        }
        // In tests, ensure the referenced order row exists to satisfy FK constraints
        if postgresdb.DB != nil {
            _, _ = postgresdb.DB.Exec(`
                INSERT INTO orders (id, customer_id, status, total_amount)
                VALUES ($1, $2, $3, $4)
                ON CONFLICT (id) DO NOTHING
            `, orderID, "customer-abc", "PENDING", 49.99)
        }
        resp := map[string]any{
            "orderId":     orderID,
            "paymentId":   "pay-" + orderID,
            "invoiceLink": fmt.Sprintf("https://example.test/invoices/%s", orderID),
            "status":      "pending",
        }
        wr.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(wr).Encode(resp)
    })
    runtimeSrv := httptest.NewServer(runtimeMux)
    w.ap2RuntimeBase = runtimeSrv.URL
    _ = os.Setenv("RESTATE_RUNTIME_URL", w.ap2RuntimeBase)
    w.debugf("[AP2-Test] Stub runtime started at %s", w.ap2RuntimeBase)

    // AP2 server mounting real handlers
    ap2Mux := http.NewServeMux()
    ap2Mux.HandleFunc("/ap2/mandates", ap2handlers.HandleCreateMandate)
    ap2Mux.HandleFunc("/ap2/intents", ap2handlers.HandleCreateIntent)
    ap2Mux.HandleFunc("/ap2/authorize", ap2handlers.HandleAuthorize)
    ap2Mux.HandleFunc("/ap2/execute", ap2handlers.HandleExecute)
    ap2Mux.HandleFunc("/ap2/status/", ap2handlers.HandleGetStatus)
    ap2Srv := httptest.NewServer(ap2Mux)
    w.ap2Base = ap2Srv.URL
    w.debugf("[AP2-Test] AP2 server started at %s", w.ap2Base)

    // Ensure servers are closed after scenario via test cleanup
    w.t.Cleanup(func() {
        ap2Srv.Close()
        runtimeSrv.Close()
    })
    return nil
}

func (w *PipelineWorld) ap2CreateIntent(customerID, cartID string) error {
    // First, ensure a mandate exists to satisfy FK constraints
    mreq := map[string]any{
        "customer_id":  customerID,
        "scope":        "payments",
        "amount_limit": 100000.0,
        "expires_at":   "2099-01-01T00:00:00Z",
    }
    mb, _ := json.Marshal(mreq)
    url := w.ap2Base + "/ap2/mandates"
    w.debugf("[AP2-Test] POST %s body=%s", url, string(mb))
    mresp, err := http.Post(url, "application/json", bytes.NewReader(mb))
    if err != nil {
        return err
    }
    defer mresp.Body.Close()
    if mresp.StatusCode != http.StatusOK {
        b, _ := io.ReadAll(mresp.Body)
        w.debugf("[AP2-Test] mandate resp status=%d body=%s", mresp.StatusCode, string(b))
        return fmt.Errorf("create mandate status %d", mresp.StatusCode)
    }
    var mj map[string]any
    _ = json.NewDecoder(mresp.Body).Decode(&mj)
    mandateID, _ := mj["mandate_id"].(string)
    if mandateID == "" {
        return fmt.Errorf("mandate_id missing in response")
    }
    w.ap2MandateID = mandateID
    w.debugf("[AP2-Test] Mandate created mandate_id=%s", mandateID)

    body := map[string]any{
        "mandate_id":  mandateID,
        "customer_id": customerID,
        "cart_id":     cartID,
    }
    b, _ := json.Marshal(body)
    url = w.ap2Base + "/ap2/intents"
    w.debugf("[AP2-Test] POST %s body=%s", url, string(b))
    resp, err := http.Post(url, "application/json", bytes.NewReader(b))
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        rb, _ := io.ReadAll(resp.Body)
        w.debugf("[AP2-Test] intent resp status=%d body=%s", resp.StatusCode, string(rb))
        return fmt.Errorf("create intent status %d", resp.StatusCode)
    }
    var j map[string]any
    _ = json.NewDecoder(resp.Body).Decode(&j)
    if s, _ := j["intent_id"].(string); s != "" { w.ap2IntentID = s } else { return fmt.Errorf("intent_id missing in response") }
    w.debugf("[AP2-Test] Intent created intent_id=%s", w.ap2IntentID)
    return nil
}

func (w *PipelineWorld) ap2AuthorizeIntent() error {
    if w.ap2IntentID == "" {
        return fmt.Errorf("no intent created")
    }
    body := map[string]any{
        "intent_id":  w.ap2IntentID,
        "mandate_id": w.ap2MandateID,
    }
    b, _ := json.Marshal(body)
    url := w.ap2Base + "/ap2/authorize"
    w.debugf("[AP2-Test] POST %s body=%s", url, string(b))
    resp, err := http.Post(url, "application/json", bytes.NewReader(b))
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        rb, _ := io.ReadAll(resp.Body)
        w.debugf("[AP2-Test] authorize resp status=%d body=%s", resp.StatusCode, string(rb))
        return fmt.Errorf("authorize status %d", resp.StatusCode)
    }
    var j map[string]any
    _ = json.NewDecoder(resp.Body).Decode(&j)
    if s, _ := j["authorization_id"].(string); s != "" {
        w.ap2AuthorizationID = s
        return nil
    }
    return fmt.Errorf("authorization_id missing in response")
}

func (w *PipelineWorld) ap2ExecuteIntent() error {
    if w.ap2AuthorizationID == "" || w.ap2IntentID == "" {
        return fmt.Errorf("missing authorization or intent id")
    }
    body := map[string]any{
        "authorization_id": w.ap2AuthorizationID,
        "intent_id":        w.ap2IntentID,
    }
    b, _ := json.Marshal(body)
    url := w.ap2Base + "/ap2/execute"
    w.debugf("[AP2-Test] POST %s body=%s", url, string(b))
    resp, err := http.Post(url, "application/json", bytes.NewReader(b))
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        rb, _ := io.ReadAll(resp.Body)
        w.debugf("[AP2-Test] execute resp status=%d body=%s", resp.StatusCode, string(rb))
        return fmt.Errorf("execute status %d", resp.StatusCode)
    }
    var j map[string]any
    _ = json.NewDecoder(resp.Body).Decode(&j)
    result, _ := j["result"].(map[string]any)
    if result == nil {
        return fmt.Errorf("missing result envelope")
    }
    w.ap2ExecutionID, _ = result["executionId"].(string)
    w.ap2OrderID, _ = result["orderId"].(string)
    w.ap2PaymentID, _ = result["paymentId"].(string)
    w.ap2InvoiceLink, _ = result["invoiceLink"].(string)
    w.debugf("[AP2-Test] Execute result exec=%s order=%s pay=%s invoice=%s", w.ap2ExecutionID, w.ap2OrderID, w.ap2PaymentID, w.ap2InvoiceLink)
    if w.ap2ExecutionID == "" || w.ap2OrderID == "" || w.ap2InvoiceLink == "" {
        return fmt.Errorf("incomplete execute response: %+v", result)
    }
    return nil
}

func (w *PipelineWorld) ap2AssertExecuteResponse() error {
    if w.ap2OrderID == "" || w.ap2InvoiceLink == "" {
        return fmt.Errorf("missing order or invoice link")
    }
    w.debugf("[AP2-Test] Assert execute OK order=%s invoice=%s", w.ap2OrderID, w.ap2InvoiceLink)
    return nil
}

func (w *PipelineWorld) ap2AssertStatus() error {
    if w.ap2ExecutionID == "" {
        return fmt.Errorf("no execution id")
    }
    url := w.ap2Base + "/ap2/status/" + w.ap2ExecutionID
    w.debugf("[AP2-Test] GET %s", url)
    resp, err := http.Get(url)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        rb, _ := io.ReadAll(resp.Body)
        w.debugf("[AP2-Test] status resp status=%d body=%s", resp.StatusCode, string(rb))
        return fmt.Errorf("status status %d", resp.StatusCode)
    }
    var j map[string]any
    _ = json.NewDecoder(resp.Body).Decode(&j)
    result, _ := j["result"].(map[string]any)
    if result == nil {
        return fmt.Errorf("missing result envelope")
    }
    execID, _ := result["executionId"].(string)
    if execID != w.ap2ExecutionID {
        return fmt.Errorf("executionId mismatch: expected %s got %s", w.ap2ExecutionID, execID)
    }
    w.debugf("[AP2-Test] Status OK exec=%s", execID)
    return nil
}

// ap2AssertCartEmpty asserts that the in-memory cart for the given customer is empty via the stub runtime API
func (w *PipelineWorld) ap2AssertCartEmpty(customerID string) error {
    if w.ap2RuntimeBase == "" {
        return fmt.Errorf("AP2 runtime not started")
    }
    // Ask the runtime for the cart contents
    url := fmt.Sprintf("%s/cart.sv1.CartService/%s/ViewCart", w.ap2RuntimeBase, customerID)
    body := map[string]any{"customer_id": customerID}
    b, _ := json.Marshal(body)
    resp, err := http.Post(url, "application/json", bytes.NewReader(b))
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        rb, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("view cart status %d: %s", resp.StatusCode, string(rb))
    }
    var j map[string]any
    _ = json.NewDecoder(resp.Body).Decode(&j)
    cs, _ := j["cart_state"].(map[string]any)
    if cs == nil {
        return fmt.Errorf("missing cart_state")
    }
    items, _ := cs["items"].([]any)
    total, _ := cs["total_amount"].(float64)
    if len(items) != 0 || total != 0 {
        return fmt.Errorf("expected empty cart, got items=%d total=%.2f", len(items), total)
    }
    w.debugf("[AP2-Test] Cart is empty for customer %s", customerID)
    return nil
}
