Feature: Checkout workflow
  Background:
    Given a clean database

  Scenario: Successful checkout with available stock
    Given merchant "merchant-001" exists with items:
      | item_id | name        | quantity | price |
      | sku-1   | Widget Pro  | 5        | 49.99 |
    And a checkout request for customer "customer-123" at merchant "merchant-001" with items:
      | product_id | quantity |
      | sku-1      | 2        |
    When the checkout workflow runs
    Then the order is persisted with status "PENDING"
    And merchant item "sku-1" now has quantity 3
    And an order item "sku-1" was recorded with quantity 2
