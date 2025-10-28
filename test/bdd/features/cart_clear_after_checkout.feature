Feature: Cart cleared after checkout
  As a shopper
  I want my cart to be emptied after checkout
  So that I don't accidentally re-order the same items

  Scenario: Cart is cleared after AP2 checkout
    Given a clean database
    And AP2 test servers are running
    And I create an AP2 intent for customer "customer-abc" with cart "cart-001"
    And I authorize the AP2 intent
    When I execute the AP2 intent
    Then the AP2 execute response contains an order and invoice link
    And the cart for customer "customer-abc" is empty

