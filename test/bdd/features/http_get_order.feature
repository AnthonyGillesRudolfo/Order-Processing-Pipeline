Feature: HTTP GET order
  Background:
    Given a clean database

  Scenario: Retrieve created order via API
    Given merchant "merchant-001" exists with items:
      | item_id | name       | quantity | price |
      | sku-1   | Widget Pro | 3        | 49.99 |
    And a checkout request for customer "customer-123" at merchant "merchant-001" with items:
      | product_id | quantity |
      | sku-1      | 1        |
    When the checkout workflow runs
    And I GET the order via API
    Then the API returns status 200
    And the API payload contains this order

