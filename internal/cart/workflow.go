package cart

import (
	"fmt"
	"log"
	"time"

	merchantpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/merchant/v1"
	restate "github.com/restatedev/sdk-go"
)

// CartItem represents an item in the shopping cart
type CartItem struct {
	ProductID string  `json:"product_id"`
	Name      string  `json:"name"`
	Quantity  int32   `json:"quantity"`
	UnitPrice float64 `json:"unit_price"`
}

// CartState represents the complete cart state
type CartState struct {
	CustomerID  string     `json:"customer_id"`
	MerchantID  string     `json:"merchant_id"`
	Items       []CartItem `json:"items"`
	TotalAmount float64    `json:"total_amount"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// AddToCartRequest represents the request to add items to cart
type AddToCartRequest struct {
	CustomerID string     `json:"customer_id"`
	MerchantID string     `json:"merchant_id"`
	Items      []CartItem `json:"items"`
}

// AddToCartResponse represents the response after adding items
type AddToCartResponse struct {
	Success   bool      `json:"success"`
	Message   string    `json:"message"`
	CartState CartState `json:"cart_state"`
}

// ViewCartRequest represents the request to view cart
type ViewCartRequest struct {
	CustomerID string `json:"customer_id"`
}

// ViewCartResponse represents the cart view response
type ViewCartResponse struct {
	CartState CartState `json:"cart_state"`
}

// UpdateCartItemRequest represents the request to update cart item
type UpdateCartItemRequest struct {
	CustomerID string `json:"customer_id"`
	ProductID  string `json:"product_id"`
	Quantity   int32  `json:"quantity"`
}

// UpdateCartItemResponse represents the response after updating cart item
type UpdateCartItemResponse struct {
	Success   bool      `json:"success"`
	Message   string    `json:"message"`
	CartState CartState `json:"cart_state"`
}

// RemoveFromCartRequest represents the request to remove items from cart
type RemoveFromCartRequest struct {
	CustomerID string   `json:"customer_id"`
	ProductIDs []string `json:"product_ids"`
}

// RemoveFromCartResponse represents the response after removing items
type RemoveFromCartResponse struct {
	Success   bool      `json:"success"`
	Message   string    `json:"message"`
	CartState CartState `json:"cart_state"`
}

// ClearCartRequest represents the request to clear cart
type ClearCartRequest struct {
	CustomerID string `json:"customer_id"`
}

// ClearCartResponse represents the response after clearing cart
type ClearCartResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// AddToCart adds items to the customer's shopping cart with stock validation
func AddToCart(ctx restate.ObjectContext, req *AddToCartRequest) (*AddToCartResponse, error) {
	customerID := restate.Key(ctx)
	log.Printf("[Cart Workflow %s] Adding items to cart for customer: %s", customerID, req.CustomerID)

	// Get existing cart state
	var cartState CartState
	existingItems, _ := restate.Get[[]CartItem](ctx, "items")
	existingMerchantID, _ := restate.Get[string](ctx, "merchant_id")
	existingTotal, _ := restate.Get[float64](ctx, "total_amount")

	cartState.CustomerID = customerID
	cartState.MerchantID = existingMerchantID
	cartState.Items = existingItems
	cartState.TotalAmount = existingTotal
	cartState.UpdatedAt = time.Now()

	// Validate merchant consistency
	if cartState.MerchantID != "" && cartState.MerchantID != req.MerchantID {
		return &AddToCartResponse{
			Success: false,
			Message: fmt.Sprintf("Cart already contains items from merchant %s, cannot add items from merchant %s", cartState.MerchantID, req.MerchantID),
		}, nil
	}

	// Set merchant ID if not set
	if cartState.MerchantID == "" {
		cartState.MerchantID = req.MerchantID
		restate.Set(ctx, "merchant_id", req.MerchantID)
	}

	// Validate stock and add items
	for _, newItem := range req.Items {
		// Get item details from merchant service
		itemReq := &merchantpb.GetItemRequest{
			MerchantId: req.MerchantID,
			ItemId:     newItem.ProductID,
		}

		merchantClient := restate.Object[*merchantpb.Item](ctx, "merchant.sv1.MerchantService", req.MerchantID, "GetItem")
		itemProto, err := merchantClient.Request(itemReq)
		if err != nil {
			return &AddToCartResponse{
				Success: false,
				Message: fmt.Sprintf("Item '%s' not found: %v", newItem.ProductID, err),
			}, nil
		}

		// Check stock availability
		if itemProto.Quantity < newItem.Quantity {
			return &AddToCartResponse{
				Success: false,
				Message: fmt.Sprintf("Insufficient stock for item '%s': requested %d, available %d", itemProto.Name, newItem.Quantity, itemProto.Quantity),
			}, nil
		}

		// Check if item already exists in cart
		found := false
		for i, existingItem := range cartState.Items {
			if existingItem.ProductID == newItem.ProductID {
				// Update existing item
				cartState.Items[i].Quantity += newItem.Quantity
				cartState.Items[i].UnitPrice = itemProto.Price
				cartState.Items[i].Name = itemProto.Name
				found = true
				break
			}
		}

		// Add new item if not found
		if !found {
			cartState.Items = append(cartState.Items, CartItem{
				ProductID: newItem.ProductID,
				Name:      itemProto.Name,
				Quantity:  newItem.Quantity,
				UnitPrice: itemProto.Price,
			})
		}

		log.Printf("[Cart Workflow %s] Added item %s (qty: %d, price: %.2f)", customerID, itemProto.Name, newItem.Quantity, itemProto.Price)
	}

	// Calculate total amount
	cartState.TotalAmount = 0
	for _, item := range cartState.Items {
		cartState.TotalAmount += float64(item.Quantity) * item.UnitPrice
	}

	// Update cart state in Restate
	restate.Set(ctx, "items", cartState.Items)
	restate.Set(ctx, "total_amount", cartState.TotalAmount)
	restate.Set(ctx, "updated_at", cartState.UpdatedAt)

	log.Printf("[Cart Workflow %s] Cart updated: %d items, total: %.2f", customerID, len(cartState.Items), cartState.TotalAmount)

	return &AddToCartResponse{
		Success:   true,
		Message:   "Items added to cart successfully",
		CartState: cartState,
	}, nil
}

// ViewCart retrieves the current cart contents
func ViewCart(ctx restate.ObjectSharedContext, req *ViewCartRequest) (*ViewCartResponse, error) {
	customerID := restate.Key(ctx)
	log.Printf("[Cart Workflow %s] Viewing cart for customer: %s", customerID, req.CustomerID)

	// Get cart state from Restate
	items, _ := restate.Get[[]CartItem](ctx, "items")
	merchantID, _ := restate.Get[string](ctx, "merchant_id")
	totalAmount, _ := restate.Get[float64](ctx, "total_amount")
	updatedAt, _ := restate.Get[time.Time](ctx, "updated_at")

	cartState := CartState{
		CustomerID:  customerID,
		MerchantID:  merchantID,
		Items:       items,
		TotalAmount: totalAmount,
		UpdatedAt:   updatedAt,
	}

	log.Printf("[Cart Workflow %s] Cart contains %d items, total: %.2f", customerID, len(cartState.Items), cartState.TotalAmount)

	return &ViewCartResponse{
		CartState: cartState,
	}, nil
}

// UpdateCartItem modifies the quantity of an item in the cart
func UpdateCartItem(ctx restate.ObjectContext, req *UpdateCartItemRequest) (*UpdateCartItemResponse, error) {
	customerID := restate.Key(ctx)
	log.Printf("[Cart Workflow %s] Updating cart item %s to quantity %d", customerID, req.ProductID, req.Quantity)

	// Get existing cart state
	items, _ := restate.Get[[]CartItem](ctx, "items")
	merchantID, _ := restate.Get[string](ctx, "merchant_id")
	totalAmount, _ := restate.Get[float64](ctx, "total_amount")

	// Find and update the item
	found := false
	for i, item := range items {
		if item.ProductID == req.ProductID {
			if req.Quantity <= 0 {
				// Remove item if quantity is 0 or negative
				items = append(items[:i], items[i+1:]...)
			} else {
				// Validate stock availability
				itemReq := &merchantpb.GetItemRequest{
					MerchantId: merchantID,
					ItemId:     req.ProductID,
				}

				merchantClient := restate.Object[*merchantpb.Item](ctx, "merchant.sv1.MerchantService", merchantID, "GetItem")
				itemProto, err := merchantClient.Request(itemReq)
				if err != nil {
					return &UpdateCartItemResponse{
						Success: false,
						Message: fmt.Sprintf("Item '%s' not found: %v", req.ProductID, err),
					}, nil
				}

				if itemProto.Quantity < req.Quantity {
					return &UpdateCartItemResponse{
						Success: false,
						Message: fmt.Sprintf("Insufficient stock for item '%s': requested %d, available %d", itemProto.Name, req.Quantity, itemProto.Quantity),
					}, nil
				}

				items[i].Quantity = req.Quantity
				items[i].UnitPrice = itemProto.Price
				items[i].Name = itemProto.Name
			}
			found = true
			break
		}
	}

	if !found {
		return &UpdateCartItemResponse{
			Success: false,
			Message: fmt.Sprintf("Item '%s' not found in cart", req.ProductID),
		}, nil
	}

	// Calculate new total amount
	totalAmount = 0
	for _, item := range items {
		totalAmount += float64(item.Quantity) * item.UnitPrice
	}

	// Update cart state
	restate.Set(ctx, "items", items)
	restate.Set(ctx, "total_amount", totalAmount)
	restate.Set(ctx, "updated_at", time.Now())

	cartState := CartState{
		CustomerID:  customerID,
		MerchantID:  merchantID,
		Items:       items,
		TotalAmount: totalAmount,
		UpdatedAt:   time.Now(),
	}

	log.Printf("[Cart Workflow %s] Cart item updated: %d items, total: %.2f", customerID, len(cartState.Items), cartState.TotalAmount)

	return &UpdateCartItemResponse{
		Success:   true,
		Message:   "Cart item updated successfully",
		CartState: cartState,
	}, nil
}

// RemoveFromCart removes items from the cart
func RemoveFromCart(ctx restate.ObjectContext, req *RemoveFromCartRequest) (*RemoveFromCartResponse, error) {
	customerID := restate.Key(ctx)
	log.Printf("[Cart Workflow %s] Removing items from cart: %v", customerID, req.ProductIDs)

	// Get existing cart state
	items, _ := restate.Get[[]CartItem](ctx, "items")
	merchantID, _ := restate.Get[string](ctx, "merchant_id")
	totalAmount, _ := restate.Get[float64](ctx, "total_amount")

	// Remove specified items
	for _, productID := range req.ProductIDs {
		for i, item := range items {
			if item.ProductID == productID {
				items = append(items[:i], items[i+1:]...)
				break
			}
		}
	}

	// Calculate new total amount
	totalAmount = 0
	for _, item := range items {
		totalAmount += float64(item.Quantity) * item.UnitPrice
	}

	// Update cart state
	restate.Set(ctx, "items", items)
	restate.Set(ctx, "total_amount", totalAmount)
	restate.Set(ctx, "updated_at", time.Now())

	cartState := CartState{
		CustomerID:  customerID,
		MerchantID:  merchantID,
		Items:       items,
		TotalAmount: totalAmount,
		UpdatedAt:   time.Now(),
	}

	log.Printf("[Cart Workflow %s] Items removed from cart: %d items remaining, total: %.2f", customerID, len(cartState.Items), cartState.TotalAmount)

	return &RemoveFromCartResponse{
		Success:   true,
		Message:   "Items removed from cart successfully",
		CartState: cartState,
	}, nil
}

// ClearCart empties the cart after successful checkout
func ClearCart(ctx restate.ObjectContext, req *ClearCartRequest) (*ClearCartResponse, error) {
	customerID := restate.Key(ctx)
	log.Printf("[Cart Workflow %s] Clearing cart for customer: %s", customerID, req.CustomerID)

	// Clear cart state
	restate.Set(ctx, "items", []CartItem{})
	restate.Set(ctx, "total_amount", 0.0)
	restate.Set(ctx, "merchant_id", "")
	restate.Set(ctx, "updated_at", time.Now())

	log.Printf("[Cart Workflow %s] Cart cleared successfully", customerID)

	return &ClearCartResponse{
		Success: true,
		Message: "Cart cleared successfully",
	}, nil
}

// GetCart is a read-only handler for cart state (alias for ViewCart)
func GetCart(ctx restate.ObjectSharedContext, req *ViewCartRequest) (*ViewCartResponse, error) {
	return ViewCart(ctx, req)
}
