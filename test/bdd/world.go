package bdd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	orderpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/go/order/v1"
	postgresdb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
	"github.com/cucumber/godog"
	restate "github.com/restatedev/sdk-go"
	"runtime"
)

type PipelineWorld struct {
    t *testing.T

	checkoutReq   *orderpb.CheckoutRequest
	checkoutRes   *orderpb.CheckoutResponse
	checkoutErr   error
	paymentReq    *orderpb.ProcessPaymentRequest
	paymentRes    *orderpb.ProcessPaymentResponse
	paymentErr    error
	runCtx        restate.RunContext
	projectRoot   string
	orderKey      string
	paymentKey    string
	awakeableKey  string
	cleanupTables bool
	merchantItems map[string]merchantItem
	expectedTotal float64

	// HTTP response capture for API tests
    httpStatus int
    httpJSON   map[string]any

    // AP2 test runtime + server
    ap2Base        string
    ap2RuntimeBase string
    // Captured ids
    ap2IntentID        string
    ap2AuthorizationID string
    ap2ExecutionID     string
    ap2OrderID         string
    ap2PaymentID       string
    ap2InvoiceLink     string
}

func NewPipelineWorld(t *testing.T) *PipelineWorld {
	root := locateProjectRoot()
	return &PipelineWorld{
		t:             t,
		runCtx:        stubRunContext{Context: context.Background()},
		projectRoot:   root,
		merchantItems: make(map[string]merchantItem),
	}
}

func (w *PipelineWorld) Register(sc *godog.ScenarioContext) {
    sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
        w.resetScenarioState()
        return ctx, nil
    })
    sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
        // Nothing persistent to clean up here; servers are per-scenario in AP2 steps
        return ctx, nil
    })

    w.registerCheckoutSteps(sc)
    w.registerPaymentSteps(sc)
    w.registerAP2Steps(sc)
}

func (w *PipelineWorld) resetScenarioState() {
	w.checkoutReq = nil
	w.checkoutRes = nil
	w.checkoutErr = nil
	w.paymentReq = nil
	w.paymentRes = nil
	w.paymentErr = nil
	w.orderKey = ""
	w.paymentKey = ""
	w.awakeableKey = ""
	w.merchantItems = make(map[string]merchantItem)
    w.expectedTotal = 0
    w.httpStatus = 0
    w.httpJSON = nil
    // AP2
    w.ap2Base = ""
    w.ap2RuntimeBase = ""
    w.ap2IntentID = ""
    w.ap2AuthorizationID = ""
    w.ap2ExecutionID = ""
    w.ap2OrderID = ""
    w.ap2PaymentID = ""
    w.ap2InvoiceLink = ""
}

func (w *PipelineWorld) debugf(format string, args ...any) {
	if os.Getenv("BDD_DEBUG") != "" {
		w.t.Logf(format, args...)
	}
}

type stubRunContext struct {
	context.Context
}

func (s stubRunContext) Log() *slog.Logger {
	return slog.Default()
}

func (s stubRunContext) Request() *restate.Request {
	return &restate.Request{}
}

var (
	dbSetupOnce sync.Once
	dbSetupErr  error
)

func (w *PipelineWorld) ensureDatabase() {
    dbSetupOnce.Do(func() {
        cfg, err := databaseConfigFromEnv()
        if err != nil {
            dbSetupErr = err
            return
        }
        if err := postgresdb.InitDatabase(cfg); err != nil {
            dbSetupErr = fmt.Errorf("failed to init database (host=%s port=%d name=%s user=%s): %w", cfg.Host, cfg.Port, cfg.Database, cfg.User, err)
            return
        }
        // Only run migrations if the core schema is not present yet
        if !schemaPresent(postgresdb.DB) {
            dbSetupErr = runMigrations(postgresdb.DB, filepath.Join(w.projectRoot, "db", "migrations"))
		} else {
			dbSetupErr = nil
		}
	})

	if dbSetupErr != nil {
		w.t.Helper()
		w.t.Skipf("skipping BDD tests: %v", dbSetupErr)
	}
}

// schemaPresent returns true if the key application tables already exist.
func schemaPresent(db *sql.DB) bool {
	// Use a small set of sentinel tables that should always be present when migrated
	return tableExists(db, "orders") && tableExists(db, "payments") && tableExists(db, "merchant_items")
}

func tableExists(db *sql.DB, name string) bool {
	if db == nil {
		return false
	}
	// Default to public schema when none specified
	if !strings.Contains(name, ".") {
		name = "public." + name
	}
	var reg sql.NullString
	// to_regclass returns NULL if the relation does not exist
	if err := db.QueryRow(`SELECT to_regclass($1)`, name).Scan(&reg); err != nil {
		return false
	}
	return reg.Valid && reg.String != ""
}

func (w *PipelineWorld) cleanDatabase() error {
    if postgresdb.DB == nil {
        return errors.New("database not initialised")
    }

    // Safety guard: avoid truncating a non-test DB unless explicitly allowed
    name := getenv("ORDER_DB_NAME", "")
    if os.Getenv("ALLOW_DB_TRUNCATE_FOR_TESTS") != "true" && name != "" && !strings.HasSuffix(strings.ToLower(name), "_test") {
        w.t.Skipf("skipping DB truncate on non-test database %q; set ALLOW_DB_TRUNCATE_FOR_TESTS=true to override", name)
        return nil
    }

    // Reset all tables that participate in checkout/payment flows
    _, err := postgresdb.DB.Exec(`
        TRUNCATE TABLE
            ap2_refunds,
            ap2_authorizations,
			ap2_executions,
			ap2_intents,
			ap2_mandates,
			shipping_preferences,
			order_items,
			orders,
			payments,
			shipments,
			merchant_items,
			merchants
		RESTART IDENTITY CASCADE
	`)
	return err
}

func databaseConfigFromEnv() (postgresdb.DatabaseConfig, error) {
	host := getenv("ORDER_DB_HOST", "localhost")
	portStr := getenv("ORDER_DB_PORT", "5432")
	name := getenv("ORDER_DB_NAME", "orderpipeline")
	user := getenv("ORDER_DB_USER", "orderpipelineadmin")
	password := os.Getenv("ORDER_DB_PASSWORD")

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return postgresdb.DatabaseConfig{}, fmt.Errorf("invalid ORDER_DB_PORT %q: %w", portStr, err)
	}

	return postgresdb.DatabaseConfig{
		Host:     host,
		Port:     port,
		Database: name,
		User:     user,
		Password: password,
	}, nil
}

func runMigrations(db *sql.DB, path string) error {
	if db == nil {
		return errors.New("database is nil")
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	var files []fs.DirEntry
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".up.sql") {
			files = append(files, entry)
		}
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	for _, file := range files {
		content, err := os.ReadFile(filepath.Join(path, file.Name()))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", file.Name(), err)
		}
		if _, err := db.Exec(string(content)); err != nil {
			return fmt.Errorf("execute migration %s: %w", file.Name(), err)
		}
	}

	return nil
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func locateProjectRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Clean(root)
}

type merchantItem struct {
	Name     string
	Quantity int32
	Price    float64
}

func (w *PipelineWorld) runAndCapture(fn func(restate.RunContext) (any, error), output any) error {
	result, err := fn(w.runCtx)
	if err != nil {
		return err
	}

	if output == nil || result == nil {
		return nil
	}

	val := reflect.ValueOf(output)
	if val.Kind() != reflect.Ptr || val.IsNil() {
		return nil
	}

	resultValue := reflect.ValueOf(result)
	if !resultValue.IsValid() {
		return nil
	}

	// Avoid panic when assigning into interface{}
	if val.Elem().Type() == resultValue.Type() {
		val.Elem().Set(resultValue)
	}

	return nil
}
