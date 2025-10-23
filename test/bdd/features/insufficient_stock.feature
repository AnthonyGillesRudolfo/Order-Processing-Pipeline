Feature: Insufficient stock
  Background:
    Given a clean database

  Scenario: Checkout fails when quantity exceeds available stock
    Given merchant "merchant-001" exists with items:
      | item_id | name       | quantity | price |
      | sku-1   | Widget Pro | 1        | 49.99 |
    And a checkout request for customer "customer-123" at merchant "merchant-001" with items:
      | product_id | quantity |
      | sku-1      | 2        |
    When the checkout workflow runs expecting failure
    Then no order was created
    And merchant item "sku-1" now has quantity 1

