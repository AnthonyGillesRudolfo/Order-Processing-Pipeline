Authorization (OpenFGA)

Overview
- OpenFGA provides relationship-based authorization for roles and actions.
- The backend integrates a lightweight client that checks permissions via OpenFGA’s HTTP API.
- The UI uses a check endpoint for fast gating of buttons and pages.

Model
- Located at `policy/authz.fga` (schema 1.1).
- Roles on `org`: admin, merchant, agent, customer (users or groups).
- `store` inherits roles from its `org` and defines `viewer` (admin or merchant or agent).
- `order` inherits roles from its `store`; `viewer` includes owner (buyer) and staff; `can_refund` is admin or merchant.

Key Relations
- order:{id} can_refund: admin or merchant of the order’s store.
- store:{id} viewer: admin/merchant/agent of the store’s org.
- store:{id} merchant: merchants of the store’s org.

Backend Integration
- Package: `internal/authz`.
  - `Client` interface with `Check(ctx, user, object, relation) (bool, error)`.
  - `NewFromEnv()` creates an OpenFGA client when `OPENFGA_API_URL` and `OPENFGA_STORE_ID` are set; otherwise a no-op client that allows all (for local dev).
  - `Require(...)` middleware wraps handlers and enforces checks based on the request.
  - `Can(...)` helper reads the principal from the request (cookie `act_as`, `X-Principal`, `X-User`, or `user:anonymous`).

Secured Endpoints
- `POST /api/orders/{id}/refund` → requires relation `can_refund` on `order:{id}`.
- `GET /api/stores/{storeID}/orders` → requires relation `viewer` on `store:{storeID}`.
- `POST /api/stores/{storeID}/items` → requires relation `merchant` on `store:{storeID}`.

Lightweight Check API (for UI)
- `GET /authz/check?object=...&relation=...` → `{allowed: boolean}` using the effective principal.

Impersonation (optional)
- The server respects an `act_as` cookie as the effective principal (for admin-only flows).
- You can set this cookie in dev tools to simulate impersonation (e.g. `act_as=user:bob`).

Local Dev Stack
1) Start OpenFGA in-memory (docker-compose includes `openfga`):
   - API: http://localhost:8081
   - Playground: http://localhost:3001
2) Create a store via the Playground or HTTP API; export its ID:
   - `export OPENFGA_STORE_ID=<your_store_id>`
   - When running in docker, set this in the `app` container env.
3) Load the model from `policy/authz.fga` using the Playground’s “Model” tab (paste and publish).
4) Seed tuples:
   - Build the image (`docker compose build app`).
   - Execute the seeder in a container with the right env:
     `docker compose run --rm -e OPENFGA_API_URL=http://openfga:8080 -e OPENFGA_STORE_ID=$OPENFGA_STORE_ID app /app/seed-authz`
   - The seeder verifies:
       - Check(user:alice, order:ord123, can_refund) = true
       - Check(user:charlie, order:ord123, can_refund) = false

Adding a New Action
1) Define a relation in the model (e.g., `define can_cancel: admin or merchant`).
2) Gate the server endpoint with `authz.Require(...)` or call `authz.Can(...)` inline.
3) In the UI, gate the button via `/authz/check` and hide/disable when denied.
4) Seed a tuple to grant the capability (if needed).

Environment
- `OPENFGA_API_URL` (e.g., `http://openfga:8080`).
- `OPENFGA_STORE_ID` (required for real checks; when unset, checks allow by default for local dev).

