package bdd

import (
    "database/sql"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "os"
    "strings"
    "testing"

    "github.com/cucumber/godog"
    "github.com/joho/godotenv"
    postgresdb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
)

func TestMain(m *testing.M) {
    // Load .env.test if present, else .env so DB and other settings are available to tests.
    // Use Overload so test values always override any shell/CI env.
    if _, err := os.Stat(".env.test"); err == nil {
        _ = godotenv.Overload(".env.test")
    } else {
        _ = godotenv.Overload()
    }
    // Ensure Kafka publish attempts fail fast during tests
    _ = os.Setenv("KAFKA_BROKERS", "127.0.0.1:1")
    // Make sure lib/pq does not try to enforce SSL unless explicitly desired
    if os.Getenv("PGSSLMODE") == "" {
        _ = os.Setenv("PGSSLMODE", "disable")
    }
    // Avoid real Xendit calls in tests unless explicitly allowed
    if os.Getenv("BDD_ALLOW_XENDIT") != "1" {
        _ = os.Unsetenv("XENDIT_SECRET_KEY")
        _ = os.Unsetenv("SECRET_KEY")
    }

    // If not provided, start a minimal local API just for tests and set API_BASE
    if os.Getenv("API_BASE") == "" {
        srv := startTestAPI()
        defer srv.Close()
        _ = os.Setenv("API_BASE", srv.URL)
        // Run tests with the server alive
        os.Exit(m.Run())
    } else {
        os.Exit(m.Run())
    }
}

// startTestAPI serves a minimal subset of the API needed by BDD tests.
// Currently implements: GET /api/orders/{id}
func startTestAPI() *httptest.Server {
    mux := http.NewServeMux()
    mux.HandleFunc("/api/orders/", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet {
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
            return
        }
        if postgresdb.DB == nil {
            http.Error(w, "db unavailable", http.StatusInternalServerError)
            return
        }
        id := strings.TrimPrefix(r.URL.Path, "/api/orders/")
        if id == "" {
            http.Error(w, "order id required", http.StatusBadRequest)
            return
        }
        var exists string
        err := postgresdb.DB.QueryRow("SELECT id FROM orders WHERE id = $1", id).Scan(&exists)
        if err == sql.ErrNoRows {
            http.Error(w, "not found", http.StatusNotFound)
            return
        }
        if err != nil {
            http.Error(w, "query error", http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(map[string]any{
            "order": map[string]any{
                "id": exists,
            },
        })
    })
    return httptest.NewServer(mux)
}

func TestBDDFeatures(t *testing.T) {
	opts := godog.Options{
		Format: "pretty",
		Paths:  []string{"features"},
		Strict: true,
	}

	suite := godog.TestSuite{
		Name: "order-processing-pipeline",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			world := NewPipelineWorld(t)
			world.Register(sc)
		},
		Options: &opts,
	}

	if suite.Run() != 0 {
		t.Fail()
	}
}
