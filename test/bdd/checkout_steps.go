package bdd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	merchantpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/merchant/v1"
	orderpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/order/v1"
	"github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/order"
	postgresdb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
	"github.com/cucumber/godog"
	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/mocks"
	"github.com/stretchr/testify/mock"
)

func (w *PipelineWorld) registerCheckoutSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a clean database$`, w.aCleanDatabase)
	sc.Step(`^merchant "([^"]+)" exists with items:$`, w.merchantExistsWithItems)
	sc.Step(`^a checkout request for customer "([^"]+)" at merchant "([^"]+)" with items:$`, w.prepareCheckoutRequest)
	sc.Step(`^the checkout workflow runs$`, w.runCheckoutWorkflow)
	sc.Step(`^the order is persisted with status "([^"]+)"$`, w.assertOrderStatus)
	sc.Step(`^merchant item "([^"]+)" now has quantity (\d+)$`, w.assertMerchantInventory)
	sc.Step(`^an order item "([^"]+)" was recorded with quantity (\d+)$`, w.assertOrderItemRecorded)
	sc.Step(`^the checkout workflow runs expecting failure$`, w.runCheckoutWorkflowExpectingFailure)
	sc.Step(`^no order was created$`, w.assertNoOrderCreated)
	sc.Step(`^I GET the order via API$`, w.getOrderViaAPI)
	sc.Step(`^the API returns status (\d+)$`, w.assertAPIStatus)
	sc.Step(`^the API payload contains this order$`, w.assertAPIPayloadOrder)
}

func (w *PipelineWorld) aCleanDatabase() error {
	w.ensureDatabase()
	return w.cleanDatabase()
}

func (w *PipelineWorld) merchantExistsWithItems(merchantID string, table *godog.Table) error {
	rows, err := tableToMaps(table)
	if err != nil {
		return err
	}

	if _, err := postgresdb.DB.Exec(`
		INSERT INTO merchants (merchant_id, name)
		VALUES ($1, $2)
		ON CONFLICT (merchant_id) DO UPDATE SET name = EXCLUDED.name
	`, merchantID, merchantID); err != nil {
		return fmt.Errorf("insert merchant: %w", err)
	}

	for _, row := range rows {
		itemID := row["item_id"]
		name := row["name"]
		qty, err := strconv.Atoi(row["quantity"])
		if err != nil {
			return fmt.Errorf("invalid quantity for %s: %w", itemID, err)
		}
		price, err := strconv.ParseFloat(row["price"], 64)
		if err != nil {
			return fmt.Errorf("invalid price for %s: %w", itemID, err)
		}

		key := merchantKey(merchantID, itemID)
		w.merchantItems[key] = merchantItem{
			Name:     name,
			Quantity: int32(qty),
			Price:    price,
		}

		if _, err := postgresdb.DB.Exec(`
			INSERT INTO merchant_items (merchant_id, item_id, name, quantity, price)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (merchant_id, item_id) DO UPDATE
				SET name = EXCLUDED.name,
				    quantity = EXCLUDED.quantity,
				    price = EXCLUDED.price
		`, merchantID, itemID, name, qty, price); err != nil {
			return fmt.Errorf("insert merchant item %s: %w", itemID, err)
		}
	}

	return nil
}

func (w *PipelineWorld) prepareCheckoutRequest(customerID, merchantID string, table *godog.Table) error {
	rows, err := tableToMaps(table)
	if err != nil {
		return err
	}

	items := make([]*orderpb.OrderItems, 0, len(rows))
	var total float64

	for _, row := range rows {
		productID := row["product_id"]
		qty, err := strconv.Atoi(row["quantity"])
		if err != nil {
			return fmt.Errorf("invalid quantity for %s: %w", productID, err)
		}

		items = append(items, &orderpb.OrderItems{
			ProductId: productID,
			Quantity:  int32(qty),
		})

		key := merchantKey(merchantID, productID)
		if item, ok := w.merchantItems[key]; ok {
			total += float64(qty) * item.Price
		}
	}

	w.checkoutReq = &orderpb.CheckoutRequest{
		CustomerId: customerID,
		MerchantId: merchantID,
		Items:      items,
	}
	w.expectedTotal = total
	return nil
}

func (w *PipelineWorld) runCheckoutWorkflow() error {
	if w.checkoutReq == nil {
		return fmt.Errorf("checkout request not prepared")
	}

	w.orderKey = uuid.New().String()
	w.paymentKey = uuid.New().String()
	w.awakeableKey = uuid.New().String()

	mockCtx := mocks.NewMockContext(w.t)

	mockCtx.EXPECT().Key().Return(w.orderKey)

	mockRand := mockCtx.EXPECT().MockRand()
	paymentUUID, _ := uuid.Parse(w.paymentKey)
	mockRand.UUID().Return(paymentUUID)

	for _, item := range w.checkoutReq.Items {
		key := merchantKey(w.checkoutReq.MerchantId, item.ProductId)
		itemInfo, ok := w.merchantItems[key]
		if !ok {
			return fmt.Errorf("merchant item not initialised for %s", key)
		}

		client := mockCtx.EXPECT().MockObjectClient("merchant.sv1.MerchantService", w.checkoutReq.MerchantId, "GetItem")
		client.RequestAndReturn(
			mock.MatchedBy(func(input interface{}) bool {
				req, ok := input.(*merchantpb.GetItemRequest)
				if !ok {
					return false
				}
				return req.MerchantId == w.checkoutReq.MerchantId && req.ItemId == item.ProductId
			}),
			&merchantpb.Item{
				ItemId:   item.ProductId,
				Name:     itemInfo.Name,
				Quantity: itemInfo.Quantity,
				Price:    itemInfo.Price,
			},
			nil,
		)
	}

	mockCtx.EXPECT().Set("customer_id", w.checkoutReq.CustomerId)
	mockCtx.EXPECT().Set("status", orderpb.OrderStatus_PENDING)
	mockCtx.EXPECT().Set("total_amount", w.expectedTotal)
	mockCtx.EXPECT().Set("merchant_id", w.checkoutReq.MerchantId)

	mockCtx.EXPECT().Set("payment_id", w.paymentKey).Twice()
	mockCtx.EXPECT().Set("payment_status", orderpb.PaymentStatus_PAYMENT_PENDING)
	invoiceURL := fmt.Sprintf("https://example.test/invoices/%s", w.paymentKey)
	mockCtx.EXPECT().Set("invoice_url", invoiceURL)
	mockCtx.EXPECT().Set("payment_awakeable_id", w.awakeableKey)
	mockCtx.EXPECT().Set("order_id", w.orderKey)

	mockCtx.EXPECT().Awakeable().Return(newAwakeableMock(w.awakeableKey, w.t))

	// Provide sane defaults for context.Context calls used by kafka writer
	mockCtx.On("Done").Maybe().Return((<-chan struct{})(nil))
	mockCtx.On("Err").Maybe().Return(nil)
	mockCtx.On("Deadline").Maybe().Return(time.Time{}, false)
	mockCtx.On("Value", mock.Anything).Maybe().Return(nil)

	w.interceptRun(mockCtx)

	paymentClient := mocks.NewMockClient(w.t)
	mockCtx.On("Object", "order.sv1.PaymentService", w.paymentKey, "ProcessPayment", mock.Anything).Return(paymentClient)

	paymentClient.On(
		"Request",
		mock.MatchedBy(func(input interface{}) bool {
			req, ok := input.(*orderpb.ProcessPaymentRequest)
			if !ok {
				return false
			}
			return req.OrderId == w.orderKey && req.Amount == w.expectedTotal
		}),
		mock.Anything,
	).Run(func(args mock.Arguments) {
		if respPtr, ok := args.Get(1).(**orderpb.ProcessPaymentResponse); ok {
			*respPtr = &orderpb.ProcessPaymentResponse{
				PaymentId:  w.paymentKey,
				Status:     orderpb.PaymentStatus_PAYMENT_PENDING,
				InvoiceUrl: invoiceURL,
			}
		}
	}).Return(nil)

	resp, err := order.Checkout(restate.WithMockContext(mockCtx), w.checkoutReq)
	w.checkoutRes = resp
	w.checkoutErr = err
	if err == nil {
		var status string
		var total float64
		var pid sql.NullString
		_ = postgresdb.DB.QueryRow(`SELECT status, total_amount, payment_id FROM orders WHERE id = $1`, resp.OrderId).Scan(&status, &total, &pid)
		w.debugf("checkout ok: order_id=%s status=%s total=%.2f payment_id=%s", resp.OrderId, status, total, pid.String)
	}
	return err
}

func (w *PipelineWorld) runCheckoutWorkflowExpectingFailure() error {
	if w.checkoutReq == nil {
		return fmt.Errorf("checkout request not prepared")
	}

	// Deterministic IDs for this scenario
	w.orderKey = uuid.New().String()
	w.paymentKey = uuid.New().String()
	w.awakeableKey = uuid.New().String()

	mockCtx := mocks.NewMockContext(w.t)

	mockCtx.EXPECT().Key().Return(w.orderKey)

	// Merchant GetItem returns the seeded quantity from DB map
	for _, item := range w.checkoutReq.Items {
		key := merchantKey(w.checkoutReq.MerchantId, item.ProductId)
		itemInfo, ok := w.merchantItems[key]
		if !ok {
			return fmt.Errorf("merchant item not initialised for %s", key)
		}

		client := mockCtx.EXPECT().MockObjectClient("merchant.sv1.MerchantService", w.checkoutReq.MerchantId, "GetItem")
		client.RequestAndReturn(
			mock.MatchedBy(func(input interface{}) bool {
				req, ok := input.(*merchantpb.GetItemRequest)
				if !ok {
					return false
				}
				return req.MerchantId == w.checkoutReq.MerchantId && req.ItemId == item.ProductId
			}),
			&merchantpb.Item{
				ItemId:   item.ProductId,
				Name:     itemInfo.Name,
				Quantity: itemInfo.Quantity, // expect this to be < requested
				Price:    itemInfo.Price,
			},
			nil,
		)
	}

	// No payment interactions expected when stock fails.
	// Intercept Run so no unexpected DB changes are executed pre-validation
	w.interceptRun(mockCtx)

	resp, err := order.Checkout(restate.WithMockContext(mockCtx), w.checkoutReq)
	w.checkoutRes = resp
	w.checkoutErr = err
	if err == nil {
		return fmt.Errorf("expected checkout to fail due to insufficient stock")
	}
	w.debugf("checkout failed as expected (insufficient stock): order_key=%s", w.orderKey)
	return nil
}

func (w *PipelineWorld) assertNoOrderCreated() error {
	var count int
	err := postgresdb.DB.QueryRow(`SELECT COUNT(*) FROM orders WHERE id = $1`, w.orderKey).Scan(&count)
	if err != nil {
		return fmt.Errorf("query orders: %w", err)
	}
	if count != 0 {
		return fmt.Errorf("expected no order row for %s, found %d", w.orderKey, count)
	}

	// Ensure no order_items for that id
	err = postgresdb.DB.QueryRow(`SELECT COUNT(*) FROM order_items WHERE order_id = $1`, w.orderKey).Scan(&count)
	if err != nil {
		return fmt.Errorf("query order_items: %w", err)
	}
	if count != 0 {
		return fmt.Errorf("expected no order_items for %s, found %d", w.orderKey, count)
	}
	return nil
}

func (w *PipelineWorld) getOrderViaAPI() error {
	if w.checkoutRes == nil || w.checkoutRes.OrderId == "" {
		return fmt.Errorf("no order to fetch via API; ensure checkout ran successfully")
	}
	base := getenv("API_BASE", "http://localhost:3000")
	url := fmt.Sprintf("%s/api/orders/%s", base, w.checkoutRes.OrderId)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	w.httpStatus = resp.StatusCode
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	w.httpJSON = body
	w.debugf("GET %s -> %d", url, resp.StatusCode)
	return nil
}

func (w *PipelineWorld) assertAPIStatus(status int) error {
	if w.httpStatus != status {
		return fmt.Errorf("expected API status %d got %d", status, w.httpStatus)
	}
	return nil
}

func (w *PipelineWorld) assertAPIPayloadOrder() error {
	if w.httpJSON == nil {
		return fmt.Errorf("no API JSON captured")
	}
	orderObj, ok := w.httpJSON["order"].(map[string]any)
	if !ok {
		return fmt.Errorf("API payload missing 'order' object")
	}
	id, _ := orderObj["id"].(string)
	if id != w.checkoutRes.OrderId {
		return fmt.Errorf("expected API order id %s got %s", w.checkoutRes.OrderId, id)
	}
	return nil
}

func (w *PipelineWorld) assertOrderStatus(expected string) error {
	if w.checkoutErr != nil {
		return fmt.Errorf("checkout failed: %w", w.checkoutErr)
	}

	var status string
	var total float64
	var paymentID sql.NullString

	err := postgresdb.DB.QueryRow(`SELECT status, total_amount, payment_id FROM orders WHERE id = $1`, w.checkoutRes.OrderId).
		Scan(&status, &total, &paymentID)
	if err != nil {
		return fmt.Errorf("query order: %w", err)
	}

	if !strings.EqualFold(status, expected) {
		return fmt.Errorf("expected status %s got %s", expected, status)
	}
	if math.Abs(total-w.expectedTotal) > 0.0001 {
		return fmt.Errorf("expected total %.2f got %.2f", w.expectedTotal, total)
	}
	if !paymentID.Valid || paymentID.String != w.paymentKey {
		return fmt.Errorf("expected payment_id %s got %s", w.paymentKey, paymentID.String)
	}
	return nil
}

func (w *PipelineWorld) assertMerchantInventory(productID string, remaining int) error {
	var quantity int
	err := postgresdb.DB.QueryRow(`
		SELECT quantity FROM merchant_items WHERE merchant_id = $1 AND item_id = $2
	`, w.checkoutReq.MerchantId, productID).Scan(&quantity)
	if err != nil {
		return fmt.Errorf("query merchant item: %w", err)
	}
	if quantity != remaining {
		return fmt.Errorf("expected remaining quantity %d got %d", remaining, quantity)
	}
	return nil
}

func (w *PipelineWorld) assertOrderItemRecorded(productID string, quantity int) error {
	var storedQty int
	err := postgresdb.DB.QueryRow(`
		SELECT quantity FROM order_items WHERE order_id = $1 AND item_id = $2
	`, w.checkoutRes.OrderId, productID).Scan(&storedQty)
	if err != nil {
		return fmt.Errorf("query order item: %w", err)
	}
	if storedQty != quantity {
		return fmt.Errorf("expected order item quantity %d got %d", quantity, storedQty)
	}
	return nil
}

func (w *PipelineWorld) interceptRun(mockCtx *mocks.MockContext) {
	respond := func(args mock.Arguments) {
		fn := args[0].(func(restate.RunContext) (any, error))
		output := args[1]
		if err := w.runAndCapture(fn, output); err != nil {
			w.t.Fatalf("Run closure returned error: %v", err)
		}
	}

	// Return nil error to the caller; the closure side-effects already ran via respond()
	mockCtx.On("Run", mock.Anything, mock.Anything).Maybe().Run(respond).Return(nil)
	mockCtx.On("Run", mock.Anything, mock.Anything, mock.Anything).Maybe().Run(func(args mock.Arguments) {
		respond(args)
	}).Return(nil)
}

func merchantKey(merchantID, itemID string) string {
	return merchantID + ":" + itemID
}

func tableToMaps(table *godog.Table) ([]map[string]string, error) {
	if len(table.Rows) == 0 {
		return nil, fmt.Errorf("table must have at least one row")
	}

	headers := make([]string, len(table.Rows[0].Cells))
	for i, cell := range table.Rows[0].Cells {
		headers[i] = strings.ToLower(strings.TrimSpace(cell.Value))
	}

	var rows []map[string]string
	for _, row := range table.Rows[1:] {
		if len(row.Cells) != len(headers) {
			return nil, fmt.Errorf("row column mismatch")
		}
		record := make(map[string]string, len(headers))
		for i, cell := range row.Cells {
			record[headers[i]] = strings.TrimSpace(cell.Value)
		}
		rows = append(rows, record)
	}
	return rows, nil
}

func newAwakeableMock(id string, t *testing.T) *mocks.MockAwakeableFuture {
	mockAwakeable := mocks.NewMockAwakeableFuture(t)
	mockAwakeable.EXPECT().Id().Return(id)
	return mockAwakeable
}
