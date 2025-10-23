Feature: Payment processing
  Background:
    Given a clean database

  Scenario: Process payment without Xendit secret
    Given order "order-501" exists for customer "customer-abc" totaling 149.97
    And a payment request for order "order-501" with amount 149.97
    When the payment object processes the request
    Then the payment record is persisted with status "PAYMENT_PENDING"
    And the payment response includes an invoice link
