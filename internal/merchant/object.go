package merchant

import (
	"fmt"
	"log"
	"strconv"

	merchantpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/go/merchant/v1"
	postgres "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/storage/postgres"
	restate "github.com/restatedev/sdk-go"
)

// getOrInitItems returns the current items list from object state or initializes it empty
func getOrInitItems(ctx restate.ObjectContext) ([]*merchantpb.Item, error) {
	items, err := restate.Get[[]*merchantpb.Item](ctx, "items")
	if err == nil {
		return items, nil
	}
	// Initialize empty list on first use
	empty := []*merchantpb.Item{}
	restate.Set(ctx, "items", empty)
	return empty, nil
}

// getItemsShared reads items with a shared context (read-only)
func getItemsShared(ctx restate.ObjectSharedContext) ([]*merchantpb.Item, error) {
	merchantID := restate.Key(ctx)

	// Check if already initialized in Restate state
	items, err := restate.Get[[]*merchantpb.Item](ctx, "items")
	if err == nil && len(items) > 0 {
		return items, nil
	}

	// Try to load from database on first access
	dbItems, dbErr := postgres.GetMerchantItems(merchantID)
	if dbErr != nil {
		log.Printf("[Merchant %s] Error loading from database: %v, returning empty", merchantID, dbErr)
		return []*merchantpb.Item{}, nil
	}

	// Return database items (they will be cached in state on next write operation)
	log.Printf("[Merchant %s] Loaded %d items from database", merchantID, len(dbItems))
	return dbItems, nil
}

// GetMerchant is a shared (read-only) handler returning merchant metadata and items
func GetMerchant(ctx restate.ObjectSharedContext, req *merchantpb.GetMerchantRequest) (*merchantpb.Merchant, error) {
	merchantID := restate.Key(ctx)
	if req.MerchantId != "" && req.MerchantId != merchantID {
		return nil, fmt.Errorf("merchant_id mismatch: key=%s req=%s", merchantID, req.MerchantId)
	}

	name, _ := restate.Get[string](ctx, "name")
	if name == "" {
		name = fmt.Sprintf("Merchant-%s", merchantID)
	}
	items, _ := getItemsShared(ctx)
	return &merchantpb.Merchant{MerchantId: merchantID, Name: name, Items: items}, nil
}

// ListItems is a shared (read-only) handler that supports simple token-based pagination
func ListItems(ctx restate.ObjectSharedContext, req *merchantpb.ListItemsRequest) (*merchantpb.ListItemsResponse, error) {
	merchantID := restate.Key(ctx)
	if req.MerchantId != "" && req.MerchantId != merchantID {
		return nil, fmt.Errorf("merchant_id mismatch: key=%s req=%s", merchantID, req.MerchantId)
	}

	items, _ := getItemsShared(ctx)

	// Parse pagination
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 50
	}
	start := 0
	if req.PageToken != "" {
		if off, err := strconv.Atoi(req.PageToken); err == nil && off >= 0 {
			start = off
		}
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}

	page := items[start:end]
	nextToken := ""
	if end < len(items) {
		nextToken = strconv.Itoa(end)
	}

	return &merchantpb.ListItemsResponse{Items: page, NextPageToken: nextToken}, nil
}

// GetItem is a shared (read-only) handler to fetch a single item by ID
func GetItem(ctx restate.ObjectSharedContext, req *merchantpb.GetItemRequest) (*merchantpb.Item, error) {
	merchantID := restate.Key(ctx)
	if req.MerchantId != "" && req.MerchantId != merchantID {
		return nil, fmt.Errorf("merchant_id mismatch: key=%s req=%s", merchantID, req.MerchantId)
	}

	items, _ := getItemsShared(ctx)
	for _, it := range items {
		if it.ItemId == req.ItemId {
			return it, nil
		}
	}
	return nil, fmt.Errorf("item not found: %s", req.ItemId)
}

// UpdateStock mutates the item stock using either absolute set or delta increment
func UpdateStock(ctx restate.ObjectContext, req *merchantpb.UpdateStockRequest) (*merchantpb.UpdateStockResponse, error) {
	merchantID := restate.Key(ctx)
	if req.MerchantId != "" && req.MerchantId != merchantID {
		return nil, fmt.Errorf("merchant_id mismatch: key=%s req=%s", merchantID, req.MerchantId)
	}

	items, _ := getOrInitItems(ctx)
	found := false
	for _, it := range items {
		if it.ItemId == req.ItemId {
			found = true
			switch u := req.StockUpdate.(type) {
			case *merchantpb.UpdateStockRequest_SetQuantity:
				it.Quantity = u.SetQuantity
			case *merchantpb.UpdateStockRequest_IncrementDelta:
				it.Quantity = it.Quantity + u.IncrementDelta
			default:
				// No-op if neither provided
			}
			// Ensure non-negative
			if it.Quantity < 0 {
				it.Quantity = 0
			}
			break
		}
	}

	if !found {
		// If item not found, create a new one with default quantity handling
		qty := int32(999)
		switch u := req.StockUpdate.(type) {
		case *merchantpb.UpdateStockRequest_SetQuantity:
			qty = u.SetQuantity
		case *merchantpb.UpdateStockRequest_IncrementDelta:
			qty = 999 + u.IncrementDelta
			if qty < 0 {
				qty = 0
			}
		}
		newItem := &merchantpb.Item{ItemId: req.ItemId, Name: req.ItemId, Quantity: qty, Price: 0}
		items = append(items, newItem)
	}

	restate.Set(ctx, "items", items)
	// Return the updated item
	for _, it := range items {
		if it.ItemId == req.ItemId {
			log.Printf("[Merchant %s] Updated stock for item %s => %d", merchantID, it.ItemId, it.Quantity)
			return &merchantpb.UpdateStockResponse{Item: it}, nil
		}
	}

	return nil, fmt.Errorf("unexpected error updating item: %s", req.ItemId)
}

// AddItem adds a new item to the merchant's inventory
func AddItem(ctx restate.ObjectContext, req *merchantpb.AddItemRequest) (*merchantpb.AddItemResponse, error) {
	merchantID := restate.Key(ctx)
	if req.MerchantId != "" && req.MerchantId != merchantID {
		return nil, fmt.Errorf("merchant_id mismatch: key=%s req=%s", merchantID, req.MerchantId)
	}

	// Validate input
	if req.ItemId == "" || req.Name == "" {
		return nil, fmt.Errorf("item_id and name are required")
	}
	if req.Price < 0 {
		return nil, fmt.Errorf("price cannot be negative")
	}
	if req.Quantity < 0 {
		return nil, fmt.Errorf("quantity cannot be negative")
	}

	// Persist to database using restate.Run for durability
	_, err := restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, postgres.AddMerchantItem(merchantID, req.ItemId, req.Name, req.Description, req.Price, req.Quantity)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to add item to database: %w", err)
	}

	// Update local state
	items, _ := getOrInitItems(ctx)

	// Check if item already exists
	for _, it := range items {
		if it.ItemId == req.ItemId {
			// Update existing item
			it.Name = req.Name
			it.Description = req.Description
			it.Price = req.Price
			it.Quantity = req.Quantity
			restate.Set(ctx, "items", items)
			log.Printf("[Merchant %s] Updated existing item %s", merchantID, req.ItemId)
			return &merchantpb.AddItemResponse{Item: it}, nil
		}
	}

	// Add new item
	newItem := &merchantpb.Item{
		ItemId:      req.ItemId,
		Name:        req.Name,
		Description: req.Description,
		Price:       req.Price,
		Quantity:    req.Quantity,
	}
	items = append(items, newItem)
	restate.Set(ctx, "items", items)

	log.Printf("[Merchant %s] Added new item %s: %s (price: %.2f, stock: %d)",
		merchantID, req.ItemId, req.Name, req.Price, req.Quantity)
	return &merchantpb.AddItemResponse{Item: newItem}, nil
}

// UpdateItem updates an existing item in the merchant's inventory
func UpdateItem(ctx restate.ObjectContext, req *merchantpb.UpdateItemRequest) (*merchantpb.UpdateItemResponse, error) {
	merchantID := restate.Key(ctx)
	if req.MerchantId != "" && req.MerchantId != merchantID {
		return nil, fmt.Errorf("merchant_id mismatch: key=%s req=%s", merchantID, req.MerchantId)
	}

	// Validate input
	if req.ItemId == "" {
		return nil, fmt.Errorf("item_id is required")
	}
	if req.Price < 0 {
		return nil, fmt.Errorf("price cannot be negative")
	}
	if req.Quantity < 0 {
		return nil, fmt.Errorf("quantity cannot be negative")
	}

	// Persist to database using restate.Run for durability
	_, err := restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, postgres.UpdateMerchantItem(merchantID, req.ItemId, req.Name, req.Description, req.Price, req.Quantity)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update item in database: %w", err)
	}

	// Update local state - load from DB if not already in state
	items, _ := getOrInitItems(ctx)
	if len(items) == 0 {
		// State is empty, try loading from database
		dbItems, dbErr := postgres.GetMerchantItems(merchantID)
		if dbErr == nil {
			items = dbItems
		}
	}

	found := false
	for _, it := range items {
		if it.ItemId == req.ItemId {
			found = true
			it.Name = req.Name
			it.Description = req.Description
			it.Price = req.Price
			it.Quantity = req.Quantity
			break
		}
	}

	if !found {
		// Item not in state but exists in DB (we just updated it), reload from DB
		dbItems, dbErr := postgres.GetMerchantItems(merchantID)
		if dbErr == nil {
			items = dbItems
			for _, it := range items {
				if it.ItemId == req.ItemId {
					found = true
					break
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("item not found: %s", req.ItemId)
		}
	}

	restate.Set(ctx, "items", items)

	log.Printf("[Merchant %s] Updated item %s: %s (price: %.2f, stock: %d)",
		merchantID, req.ItemId, req.Name, req.Price, req.Quantity)
	return &merchantpb.UpdateItemResponse{Item: &merchantpb.Item{
		ItemId:      req.ItemId,
		Name:        req.Name,
		Description: req.Description,
		Price:       req.Price,
		Quantity:    req.Quantity,
	}}, nil
}

// DeleteItem removes an item from the merchant's inventory
func DeleteItem(ctx restate.ObjectContext, req *merchantpb.DeleteItemRequest) (*merchantpb.DeleteItemResponse, error) {
	merchantID := restate.Key(ctx)
	if req.MerchantId != "" && req.MerchantId != merchantID {
		return nil, fmt.Errorf("merchant_id mismatch: key=%s req=%s", merchantID, req.MerchantId)
	}

	if req.ItemId == "" {
		return nil, fmt.Errorf("item_id is required")
	}

	// Persist to database using restate.Run for durability
	_, err := restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
		return nil, postgres.DeleteMerchantItem(merchantID, req.ItemId)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to delete item from database: %w", err)
	}

	// Update local state
	items, _ := getOrInitItems(ctx)
	found := false
	for i, it := range items {
		if it.ItemId == req.ItemId {
			found = true
			// Remove item from slice
			items = append(items[:i], items[i+1:]...)
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("item not found: %s", req.ItemId)
	}

	restate.Set(ctx, "items", items)

	log.Printf("[Merchant %s] Deleted item %s", merchantID, req.ItemId)
	return &merchantpb.DeleteItemResponse{Success: true}, nil
}
