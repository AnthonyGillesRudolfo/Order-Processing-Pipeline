Feature: AP2 integration
  Exercise AP2 endpoints against stubbed Restate runtime

  Background:
    Given a clean database
    And AP2 test servers are running

  Scenario: Create, authorize, execute and fetch status
    When I create an AP2 intent for customer "customer-abc" with cart "cart-123"
    And I authorize the AP2 intent
    And I execute the AP2 intent
    Then the AP2 execute response contains an order and invoice link
    And AP2 status for the execution is available

