-- AP2 Mandates
CREATE TABLE ap2_mandates (
    id VARCHAR(255) PRIMARY KEY,
    customer_id VARCHAR(255) NOT NULL,
    scope TEXT,
    amount_limit DECIMAL(10,2),
    expires_at TIMESTAMP,
    status VARCHAR(50),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- AP2 Intents
CREATE TABLE ap2_intents (
    id VARCHAR(255) PRIMARY KEY,
    mandate_id VARCHAR(255) REFERENCES ap2_mandates(id),
    customer_id VARCHAR(255) NOT NULL,
    cart_id VARCHAR(255),
    total_amount DECIMAL(10,2),
    status VARCHAR(50),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- AP2 Executions
CREATE TABLE ap2_executions (
    id VARCHAR(255) PRIMARY KEY,
    intent_id VARCHAR(255) REFERENCES ap2_intents(id),
    authorization_id VARCHAR(255),
    order_id VARCHAR(255) REFERENCES orders(id),
    payment_id VARCHAR(255) REFERENCES payments(id),
    status VARCHAR(50),
    invoice_url TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- AP2 Authorizations
CREATE TABLE ap2_authorizations (
    id VARCHAR(255) PRIMARY KEY,
    intent_id VARCHAR(255) REFERENCES ap2_intents(id),
    mandate_id VARCHAR(255) REFERENCES ap2_mandates(id),
    authorized BOOLEAN NOT NULL,
    message TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- AP2 Refunds
CREATE TABLE ap2_refunds (
    id VARCHAR(255) PRIMARY KEY,
    execution_id VARCHAR(255) REFERENCES ap2_executions(id),
    amount DECIMAL(10,2),
    reason TEXT,
    status VARCHAR(50),
    refund_id VARCHAR(255),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Shipping Preferences
CREATE TABLE shipping_preferences (
    customer_id VARCHAR(255) PRIMARY KEY,
    address_line1 VARCHAR(255),
    address_line2 VARCHAR(255),
    city VARCHAR(100),
    state VARCHAR(100),
    postal_code VARCHAR(20),
    country VARCHAR(100),
    delivery_method VARCHAR(50),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes for better performance
CREATE INDEX idx_ap2_mandates_customer_id ON ap2_mandates(customer_id);
CREATE INDEX idx_ap2_mandates_status ON ap2_mandates(status);
CREATE INDEX idx_ap2_intents_customer_id ON ap2_intents(customer_id);
CREATE INDEX idx_ap2_intents_mandate_id ON ap2_intents(mandate_id);
CREATE INDEX idx_ap2_executions_intent_id ON ap2_executions(intent_id);
CREATE INDEX idx_ap2_executions_order_id ON ap2_executions(order_id);
CREATE INDEX idx_ap2_executions_payment_id ON ap2_executions(payment_id);
CREATE INDEX idx_ap2_authorizations_intent_id ON ap2_authorizations(intent_id);
CREATE INDEX idx_ap2_refunds_execution_id ON ap2_refunds(execution_id);
