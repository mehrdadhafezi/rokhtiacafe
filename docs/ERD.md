# ERD — Rokh Tia Café

Column details and constraints: [DATABASE.md](DATABASE.md). Audit FKs to `users`
(`created_by`, `voided_by`, `accepted_by`, …) are omitted from the diagram for readability.

```mermaid
erDiagram
  users ||--o{ sessions : "has"
  users ||--o{ push_subscriptions : "registers"
  users ||--o{ audit_logs : "acts in"

  categories ||--o{ products : "groups"
  products ||--o| inventory_balances : "TRACKED only"
  products ||--o{ inventory_movements : "ledger"
  products ||--o{ purchase_entries : "bought as"
  products ||--o{ order_items : "sold as"

  orders ||--|{ order_items : "contains"
  orders ||--o{ payments : "settled by"
  orders ||--o{ inventory_movements : "SALE / WASTE"
  payments |o--o| payments : "REFUND returns PAYMENT"
  purchase_entries ||--|{ inventory_movements : "PURCHASE / PURCHASE_VOID"

  push_subscriptions ||--o{ notification_events : "delivers"
  products ||--o{ notification_events : "about"

  users {
    bigint id PK
    text username UK
    text role "ADMIN|OPERATOR"
    bool is_active
  }
  sessions {
    bytea token_hash PK
    bigint user_id FK
    text csrf_token
    timestamptz expires_at
  }
  categories {
    bigint id PK
    text name UK
    int sort_order
    bool is_active
  }
  products {
    bigint id PK
    bigint category_id FK
    text name
    bigint price_rial
    text inventory_mode "TRACKED|UNTRACKED"
    int low_stock_threshold
    bigint estimated_unit_cost_rial "UNTRACKED only"
    bool is_active
    bool is_available
  }
  inventory_balances {
    bigint product_id PK,FK
    int on_hand
    int reserved
    bigint inventory_value_rial
    bigint last_unit_cost_rial
    timestamptz low_stock_alerted_at
  }
  inventory_movements {
    bigint id PK
    bigint product_id FK
    text movement_type "PURCHASE|PURCHASE_VOID|SALE|WASTE|ADJUSTMENT_IN|ADJUSTMENT_OUT"
    text reason_code
    int quantity_delta
    bigint value_delta_rial
    int on_hand_after
    bigint order_id FK
    bigint purchase_entry_id FK
  }
  purchase_entries {
    bigint id PK
    bigint product_id FK
    int quantity
    bigint unit_cost_rial
    uuid idempotency_key UK
    timestamptz voided_at
  }
  orders {
    bigint id PK
    date business_date
    int display_number
    bytea tracking_token_hash UK
    uuid idempotency_key UK
    bytea client_key
    text status
    bigint total_rial
    bigint paid_rial
    bigint refunded_rial
    text payment_status "GENERATED"
    timestamptz expires_at
  }
  order_items {
    bigint id PK
    bigint order_id FK
    bigint product_id FK
    bigint unit_price_rial
    int quantity
    bigint line_total_rial
    bigint cogs_rial
    bigint estimated_cost_rial
    int wasted_quantity
  }
  payments {
    bigint id PK
    bigint order_id FK
    text kind "PAYMENT|REFUND"
    text method "CASH|CARD_TERMINAL"
    bigint amount_rial
    bigint refund_of_payment_id FK,UK
    uuid idempotency_key UK
    timestamptz voided_at "PAYMENT only"
  }
  expenses {
    bigint id PK
    text category
    bigint amount_rial
    date expense_date
    timestamptz voided_at
  }
  push_subscriptions {
    bigint id PK
    bigint user_id FK
    text endpoint UK
  }
  notification_events {
    bigint id PK
    text dedup_key
    bigint subscription_id FK
    text status "PENDING|SENDING|SENT|FAILED|GONE"
    int attempts
    uuid claim_token
    timestamptz lease_until
  }
  audit_logs {
    bigint id PK
    bigint actor_user_id FK
    text action
    text entity_type
    text entity_id
    jsonb details
  }
```

## Migration order
1. `users`, `sessions`
2. `categories`, `products`, `inventory_balances`
3. `purchase_entries`, `orders`, `order_items`, `inventory_movements`
4. `payments`, `expenses`
5. `push_subscriptions`, `notification_events`, `audit_logs`
6. Triggers (`forbid_delete`, `forbid_update`, `order_items_guard`, `orders_guard`, `void_only_update`, `set_updated_at`)
7. Grants: `rokh_owner` owns everything; `rokh_app` gets runtime grants ([DATABASE.md §6.1](DATABASE.md#61-roles)). The two roles themselves are created once by the setup script (local: docker-compose init; server: host tooling), not by migrations.
