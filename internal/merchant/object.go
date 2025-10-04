package merchant

import (
    "fmt"
    "log"
    "strconv"

    merchantpb "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/gen/merchant/v1"
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
    items, err := restate.Get[[]*merchantpb.Item](ctx, "items")
    if err != nil {
        return []*merchantpb.Item{}, nil
    }
    return items, nil
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


