let PRODUCTS = []; // Will be loaded dynamically
let currentMerchantId = 'm_001'; // Default merchant
let currentCustomerId = 'customer-001'; // Default customer
  
  const basket = new Map(); // productId -> quantity (local cache)
  
  const $ = (sel) => document.querySelector(sel);
  const productsEl = $('#products');
  const basketEmptyEl = $('#basket-empty');
  const basketTableEl = $('#basket-table');
  const basketBodyEl = $('#basket-body');
  const checkoutBtn = $('#checkout-btn');
  const customerInput = $('#customer-id');
  const merchantSelect = $('#merchant-select');
  const refreshProductsBtn = $('#refresh-products');
  const loadCartBtn = $('#load-cart-btn');
  const cartRefreshBtn = $('#cart-refresh-btn');
  
  const ordersListEl = document.getElementById('orders-list');
  // AuthZ controls
  const actAsInput = document.getElementById('act-as-input');
  const actAsApply = document.getElementById('act-as-apply');
  const actAsStatus = document.getElementById('act-as-status');
  const roleSelect = document.getElementById('role-select');
  const authzHint = document.getElementById('authz-hint');

    // Tambahkan setelah deklarasi elemen DOM
    const modalEl = document.getElementById('modal');
    const modalTitleEl = document.getElementById('modal-title');
    const modalBodyEl = document.getElementById('modal-body');
    const modalCloseBtn = document.getElementById('modal-close');

    function showModal(title, html) {
        modalTitleEl.textContent = title;
        modalBodyEl.innerHTML = html;
        modalEl.classList.remove('hidden');
    }
    function hideModal() { modalEl.classList.add('hidden'); }
    modalCloseBtn.onclick = hideModal;
    modalEl.addEventListener('click', (e) => { if (e.target === modalEl) hideModal(); });
  
  function renderProducts() {
    productsEl.innerHTML = '';
    PRODUCTS.forEach(p => {
      const card = document.createElement('div');
      card.className = 'card';
      card.innerHTML = `
        <div class="title">${p.name}</div>
        <div class="price">$${p.price.toFixed(2)}</div>
        <div class="stock">Stock: ${p.quantity}</div>
        <div class="card-actions">
          <button ${p.quantity <= 0 ? 'disabled' : ''}>Add to basket</button>
          <button onclick="editItem('${p.itemId}')" style="margin-left: 4px;">Edit</button>
        </div>
      `;
      if (p.quantity > 0) {
        card.querySelector('button').onclick = () => addToBasket(p.itemId);
      }
      productsEl.appendChild(card);
    });
  }

  // --- Authz helper ---
  async function authzCheck(object, relation) {
    try {
      // Optional principal from localStorage for demo/impersonation
      const p = (localStorage.getItem('principal') || '').trim();
      const qp = new URLSearchParams({ object, relation });
      if (p) qp.set('principal', p);
      const res = await fetch(`/authz/check?${qp.toString()}`);
      if (!res.ok) return false;
      const j = await res.json();
      return !!j.allowed;
    } catch { return false }
  }

  function setActAsCookie(principal) {
    if (principal) {
      document.cookie = `act_as=${principal}; path=/`;
      localStorage.setItem('principal', principal);
      actAsStatus.textContent = `Acting as ${principal}`;
    } else {
      // Clear cookie
      document.cookie = 'act_as=; Max-Age=0; path=/';
      localStorage.removeItem('principal');
      actAsStatus.textContent = 'Anonymous';
    }
  }

  function toStoreKey(merchantId) {
    // Map m_001 -> merchant-001 (store id namespace)
    if (merchantId && merchantId.startsWith('m_')) {
      return 'merchant-' + merchantId.slice(2);
    }
    return merchantId;
  }
  
  function renderBasket() {
    const items = Array.from(basket.entries());
    if (items.length === 0) {
      basketEmptyEl.classList.remove('hidden');
      basketTableEl.classList.add('hidden');
      document.getElementById('total-row').classList.add('hidden');
      checkoutBtn.disabled = true;
    } else {
      basketEmptyEl.classList.add('hidden');
      basketTableEl.classList.remove('hidden');
      document.getElementById('total-row').classList.remove('hidden');
      checkoutBtn.disabled = false;
    }
  
    basketBodyEl.innerHTML = '';
    let totalAmount = 0;
    
    items.forEach(([pid, qty]) => {
      const p = PRODUCTS.find(pp => pp.itemId === pid);
      if (!p) return; // Skip if product not found
      
      const subtotal = p.price * qty;
      totalAmount += subtotal;
      
      const tr = document.createElement('tr');
      tr.innerHTML = `
        <td>${p.name}</td>
        <td>
          <button class="qty" data-delta="-1">-</button>
          <span class="q">${qty}</span>
          <button class="qty" data-delta="1">+</button>
        </td>
        <td>$${p.price.toFixed(2)}</td>
        <td>$${subtotal.toFixed(2)}</td>
        <td><button class="remove">Remove</button></td>
      `;
      tr.querySelectorAll('.qty').forEach(btn => {
        btn.onclick = () => changeQty(pid, parseInt(btn.dataset.delta, 10));
      });
      tr.querySelector('.remove').onclick = () => removeFromBasket(pid);
      basketBodyEl.appendChild(tr);
    });
    
    // Update total
    document.getElementById('basket-total').textContent = `$${totalAmount.toFixed(2)}`;
  }
  
  async function addToBasket(pid) {
    try {
      const response = await fetch(`/api/cart/${currentCustomerId}/add`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          customer_id: currentCustomerId,
          merchant_id: currentMerchantId,
          items: [{ product_id: pid, quantity: 1 }]
        })
      });

      if (!response.ok) {
        const error = await response.text();
        showModal('Error', `Failed to add item to cart: ${error}`);
        return;
      }

      const result = await response.json();
      if (result.success) {
        // Update local cache
        basket.set(pid, (basket.get(pid) || 0) + 1);
        renderBasket();
        showModal('Success', 'Item added to cart successfully');
      } else {
        showModal('Error', result.message || 'Failed to add item to cart');
      }
    } catch (error) {
      showModal('Error', `Network error: ${error.message}`);
    }
  }
  async function changeQty(pid, delta) {
    const currentQty = basket.get(pid) || 0;
    const next = currentQty + delta;
    
    if (next <= 0) {
      // Remove item from cart
      try {
        const response = await fetch(`/api/cart/${currentCustomerId}/remove`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            customer_id: currentCustomerId,
            product_ids: [pid]
          })
        });

        if (response.ok) {
          basket.delete(pid);
          renderBasket();
        } else {
          showModal('Error', 'Failed to remove item from cart');
        }
      } catch (error) {
        showModal('Error', `Network error: ${error.message}`);
      }
    } else {
      // Update quantity
      try {
        const response = await fetch(`/api/cart/${currentCustomerId}/update`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            customer_id: currentCustomerId,
            product_id: pid,
            quantity: next
          })
        });

        if (response.ok) {
          basket.set(pid, next);
          renderBasket();
        } else {
          showModal('Error', 'Failed to update item quantity');
        }
      } catch (error) {
        showModal('Error', `Network error: ${error.message}`);
      }
    }
  }
  async function removeFromBasket(pid) {
    try {
      const response = await fetch(`/api/cart/${currentCustomerId}/remove`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          customer_id: currentCustomerId,
          product_ids: [pid]
        })
      });

      if (response.ok) {
        basket.delete(pid);
        renderBasket();
      } else {
        showModal('Error', 'Failed to remove item from cart');
      }
    } catch (error) {
      showModal('Error', `Network error: ${error.message}`);
    }
  }
  
  async function checkout() {
    const customer_id = customerInput.value.trim() || 'customer-001';
    const items = Array.from(basket.entries()).map(([product_id, quantity]) => ({ product_id, quantity }));
    const merchant_id = currentMerchantId;
  
    
    showModal('Order invoked', 'Sending request to Restate runtime...');
    checkoutBtn.disabled = true;
  
    try {
      const res = await fetch('/api/checkout', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ customer_id, items, merchant_id }),
      });
  
      if (!res.ok) {
        let detail = '';
        let errorMessage = 'Checkout failed';
        try { 
          const j = await res.json(); 
          if (j.detail) {
            detail = `<pre>${JSON.stringify(j.detail, null, 2)}</pre>`;
            // Check if it's a stock validation error
            if (j.detail.error && j.detail.error.includes('Insufficient stock')) {
              errorMessage = 'Insufficient Stock';
              detail = `<div style="color: #dc3545; font-weight: bold;">${j.detail.error}</div>`;
            } else if (j.detail.error && j.detail.error.includes('Item') && j.detail.error.includes('not found')) {
              errorMessage = 'Item Not Found';
              detail = `<div style="color: #dc3545; font-weight: bold;">${j.detail.error}</div>`;
            }
          }
        } catch {}
        showModal(errorMessage, `${errorMessage === 'Checkout failed' ? 'Make sure the Restate runtime is active & deployment is registered.' : ''}${detail}`);
        return;
      }
  
      const { order_id, invoice_url } = await res.json();
      basket.clear();
      renderBasket();
  
      // Popup + invoice link
      if (invoice_url) showModal('Order processed', `Invocation success.<br/>Order ID: <code>${order_id}</code><br/>Invoice: <a href="${invoice_url}" target="_blank" rel="noopener">open</a>`);
      else showModal('Order processed', `Invocation success.<br/>Order ID: <code>${order_id}</code>`);
      // Persist so order survives refresh
      try {
        localStorage.setItem('last_order_id', order_id);
        if (invoice_url) localStorage.setItem('last_invoice_url', invoice_url);
      } catch {}
      await loadOrders();
    } catch (e) {
      showModal('Network error', String(e));
    } finally {
      checkoutBtn.disabled = false;
    }
  }
  
  // Remove per-order polling; we list all orders instead
  
  async function loadProducts() {
    try {
      const response = await fetch(`/api/merchants/${currentMerchantId}/items`);
      if (!response.ok) {
        throw new Error(`Failed to load products: ${response.status}`);
      }
      const data = await response.json();
      PRODUCTS = data.items || [];
      renderProducts();
    } catch (error) {
      console.error('Error loading products:', error);
      showModal('Error', `Failed to load products: ${error.message}`);
      // Fallback to empty products
      PRODUCTS = [];
      renderProducts();
    }
  }
  
  checkoutBtn.onclick = checkout;
  
  // Merchant selector event handlers
  merchantSelect.onchange = () => {
    currentMerchantId = merchantSelect.value;
    basket.clear(); // Clear basket when switching merchants
    loadProducts();
    loadCart(); // Reload cart from backend
    gateMerchantUI();
  };
  
  refreshProductsBtn.onclick = () => {
    loadProducts();
  };

  // Cart management buttons
  loadCartBtn.onclick = () => {
    const newCustomerId = customerInput.value.trim() || 'customer-001';
    if (newCustomerId !== currentCustomerId) {
      currentCustomerId = newCustomerId;
      basket.clear(); // Clear local basket when switching customers
    }
    loadCart();
  };

  cartRefreshBtn.onclick = () => {
    loadCart();
  };
  
  // Load cart from backend
  async function loadCart() {
    try {
      console.log(`Loading cart for customer: ${currentCustomerId}`);
      const response = await fetch(`/api/cart/${currentCustomerId}/view`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ customer_id: currentCustomerId })
      });

      if (response.ok) {
        const result = await response.json();
        console.log('Cart loaded successfully:', result);
        const cartState = result.cart_state || {};
        const items = cartState.items || [];
        
        // Update local basket cache
        basket.clear();
        items.forEach(item => {
          basket.set(item.product_id, item.quantity);
        });
        
        renderBasket();
        console.log(`Cart rendered with ${items.length} items`);
      } else {
        console.warn('Failed to load cart from backend:', response.status, response.statusText);
        const errorText = await response.text();
        console.warn('Error details:', errorText);
        renderBasket(); // Render empty cart
      }
    } catch (error) {
      console.warn('Error loading cart:', error);
      renderBasket(); // Render empty cart
    }
  }

  // Load products and initialize
  loadProducts();
  // Add a small delay to ensure the server is ready
  setTimeout(() => {
    loadCart();
  }, 500);
  loadOrders();
  gateMerchantUI();

  async function gateMerchantUI() {
    const addBtn = document.getElementById('add-item-btn');
    const storeKey = toStoreKey(currentMerchantId);
    const isMerchant = await authzCheck(`store:${storeKey}`, 'merchant');
    if (isMerchant) {
      addBtn.classList.remove('hidden');
      if (authzHint) authzHint.textContent = 'You can add items (merchant)';
    } else {
      addBtn.classList.add('hidden');
      if (authzHint) authzHint.textContent = 'Add item hidden (not merchant)';
    }
  }

  // Wire up impersonation controls
  (function initAuthZControls() {
    const saved = localStorage.getItem('principal') || '';
    if (actAsInput) actAsInput.value = saved;
    if (roleSelect) {
      // Set dropdown based on saved principal
      roleSelect.value = saved || '';
    }
    setActAsCookie(saved);

    if (actAsApply) actAsApply.onclick = async () => {
      const p = (actAsInput.value || '').trim();
      setActAsCookie(p);
      if (roleSelect) roleSelect.value = p;
      await gateMerchantUI();
    };

    if (roleSelect) roleSelect.onchange = async () => {
      const p = roleSelect.value;
      if (actAsInput) actAsInput.value = p;
      setActAsCookie(p);
      await gateMerchantUI();
    };
  })();

  async function loadOrders() {
    try {
      const res = await fetch('/api/orders');
      if (!res.ok) return;
      const data = await res.json();
      const orders = data.orders || [];
      ordersListEl.innerHTML = '';
      orders.forEach(async (o) => {
        const div = document.createElement('div');
        div.className = 'order-card';
        const items = (o.items || []).map(it => `${(it.name || it.product_id)} x${it.quantity}`).join(', ');
        const inv = o.invoice_url ? `<a href="${o.invoice_url}" target="_blank" rel="noopener">invoice</a>` : '-';
        let actions = `
          <div><b>ID</b>: <code>${o.id}</code></div>
          <div><b>Status</b>: ${o.status} | <b>Payment</b>: ${o.payment_status || '-'}</div>
          <div><b>Items</b>: ${items || '-'}</div>
          <div><b>Invoice</b>: ${inv}</div>
          <div class="order-actions">
            ${o.status === 'PROCESSING' ? 
              `<button onclick="shipOrder('${o.id}')" style="background-color: #007bff; color: white; border: none; padding: 4px 8px; border-radius: 4px; cursor: pointer; margin-right: 8px;">Ship Order (Auto-Deliver)</button>` : ''}
            ${o.status === 'SHIPPED' ? 
              `<span style="color: #28a745; font-weight: bold; margin-right: 8px;">In Transit...</span>
               <button onclick="deliverOrder('${o.id}')" style="background-color: #ffc107; color: black; border: none; padding: 4px 8px; border-radius: 4px; cursor: pointer; margin-right: 8px;">Manual Deliver</button>` : ''}
            ${o.status === 'PROCESSING' || o.status === 'SHIPPED' ? 
              `<button onclick="cancelOrder('${o.id}')" style="background-color: #dc3545; color: white; border: none; padding: 4px 8px; border-radius: 4px; cursor: pointer;">Cancel Order</button>` : ''}
            ${o.status === 'DELIVERED' ? 
              `<button onclick="confirmOrder('${o.id}')" style="background-color: #28a745; color: white; border: none; padding: 4px 8px; border-radius: 4px; cursor: pointer; margin-right: 8px;">Confirm Order</button>
               <button onclick="returnOrder('${o.id}')" style="background-color: #dc3545; color: white; border: none; padding: 4px 8px; border-radius: 4px; cursor: pointer;">Return Order</button>` : ''}
            ${o.status === 'COMPLETED' ? 
              `<span style="color: #28a745; font-weight: bold;">✓ Order Completed</span>` : ''}
            ${o.status === 'RETURNED' ? 
              `<span style="color: #dc3545; font-weight: bold;">↩ Order Returned</span>` : ''}
          </div>`;
        // Gate Refund button by check(order:{id}, can_refund)
        const allowRefund = await authzCheck(`order:${o.id}`, 'can_refund');
        if (o.status === 'DELIVERED' && allowRefund) {
          actions = actions.replace('</div>`','') + `
            <button onclick="refundOrder('${o.id}')" style="background-color: #6c757d; color: white; border: none; padding: 4px 8px; border-radius: 4px; cursor: pointer; margin-left: 8px;">Refund</button>
          </div>`;
        }
        div.innerHTML = actions;
        ordersListEl.appendChild(div);
      });
    } catch {}
  }

  // No per-order resume needed; orders list shows all

  // Merchant Item Management
  let currentEditingItem = null;
  const itemModalEl = document.getElementById('item-modal');
  const itemFormEl = document.getElementById('item-form');
  const itemSaveBtn = document.getElementById('item-save-btn');
  const itemDeleteBtn = document.getElementById('item-delete-btn');
  const itemCancelBtn = document.getElementById('item-cancel-btn');
  const addItemBtn = document.getElementById('add-item-btn');

  function showItemModal() {
    currentEditingItem = null;
    itemFormEl.reset();
    itemDeleteBtn.classList.add('hidden');
    document.getElementById('item-modal-title').textContent = 'Add New Item';
    itemModalEl.classList.remove('hidden');
  }

  function hideItemModal() {
    itemModalEl.classList.add('hidden');
    currentEditingItem = null;
  }

  function editItem(itemId) {
    const item = PRODUCTS.find(p => p.itemId === itemId);
    if (!item) return;
    
    currentEditingItem = item;
    document.getElementById('item-id').value = item.itemId;
    document.getElementById('item-name').value = item.name;
    document.getElementById('item-price').value = item.price;
    document.getElementById('item-quantity').value = item.quantity;
    
    document.getElementById('item-modal-title').textContent = 'Edit Item';
    itemDeleteBtn.classList.remove('hidden');
    itemModalEl.classList.remove('hidden');
  }

  async function saveItem() {
    const itemId = document.getElementById('item-id').value;
    const name = document.getElementById('item-name').value;
    const price = parseFloat(document.getElementById('item-price').value);
    const quantity = parseInt(document.getElementById('item-quantity').value);

    if (!itemId || !name || isNaN(price) || isNaN(quantity)) {
      showModal('Error', 'Please fill in all fields with valid values');
      return;
    }

    try {
      const url = `/api/merchants/${currentMerchantId}/items/${itemId}`;
      const method = currentEditingItem ? 'PUT' : 'POST';
      
      const response = await fetch(url, {
        method: method,
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          merchant_id: currentMerchantId,
          item_id: itemId,
          name: name,
          price: price,
          quantity: quantity
        })
      });

      if (!response.ok) {
        throw new Error(`Failed to ${currentEditingItem ? 'update' : 'add'} item: ${response.status}`);
      }

      hideItemModal();
      await loadProducts();
      showModal('Success', `Item ${currentEditingItem ? 'updated' : 'added'} successfully`);
    } catch (error) {
      showModal('Error', `Failed to save item: ${error.message}`);
    }
  }

  async function deleteItem() {
    if (!currentEditingItem) return;
    
    if (!confirm(`Are you sure you want to delete "${currentEditingItem.name}"?`)) {
      return;
    }

    try {
      const response = await fetch(`/api/merchants/${currentMerchantId}/items/${currentEditingItem.itemId}`, {
        method: 'DELETE'
      });

      if (!response.ok) {
        throw new Error(`Failed to delete item: ${response.status}`);
      }

      hideItemModal();
      await loadProducts();
      showModal('Success', 'Item deleted successfully');
    } catch (error) {
      showModal('Error', `Failed to delete item: ${error.message}`);
    }
  }

  // Event listeners
  addItemBtn.onclick = showItemModal;
  itemSaveBtn.onclick = saveItem;
  itemDeleteBtn.onclick = deleteItem;
  itemCancelBtn.onclick = hideItemModal;
  itemModalEl.addEventListener('click', (e) => { if (e.target === itemModalEl) hideItemModal(); });

  // Order cancellation
  async function cancelOrder(orderId) {
    const reason = prompt('Please enter a cancellation reason:');
    if (!reason) return;

    try {
      const response = await fetch(`/api/orders/${orderId}/cancel`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ reason: reason })
      });

      if (!response.ok) {
        throw new Error(`Failed to cancel order: ${response.status}`);
      }

      showModal('Success', 'Order cancelled successfully');
      await loadOrders();
    } catch (error) {
      showModal('Error', `Failed to cancel order: ${error.message}`);
    }
  }

  // Make functions globally available
  window.cancelOrder = cancelOrder;

  // Order shipping
  async function shipOrder(orderId) {
    const trackingNumber = prompt('Enter tracking number (optional):') || '';
    const carrier = prompt('Enter carrier (default: FedEx):') || 'FedEx';
    const serviceType = prompt('Enter service type (default: Ground):') || 'Ground';

    try {
      const response = await fetch(`/api/orders/${orderId}/ship`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ 
          tracking_number: trackingNumber,
          carrier: carrier,
          service_type: serviceType
        })
      });

      if (!response.ok) {
        throw new Error(`Failed to ship order: ${response.status}`);
      }

      showModal('Shipping Started', 'Order is being shipped and will be automatically delivered in 5 seconds.');
      await loadOrders();
      
      // Schedule automatic delivery with retry logic
      scheduleAutomaticDelivery(orderId);
    } catch (error) {
      showModal('Error', `Failed to ship order: ${error.message}`);
    }
  }

  // Robust automatic delivery with retry logic
  async function scheduleAutomaticDelivery(orderId, attempt = 1, maxAttempts = 3) {
    const delay = 5000; // 5 seconds initial delay
    
    setTimeout(async () => {
      try {
        console.log(`Attempting automatic delivery for order ${orderId} (attempt ${attempt}/${maxAttempts})`);
        
        const deliveryResponse = await fetch(`/api/orders/${orderId}/deliver`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({})
        });
        
        if (deliveryResponse.ok) {
          console.log(`Order ${orderId} automatically delivered successfully`);
          await loadOrders(); // Refresh the order list to show the updated status
        } else {
          const errorText = await deliveryResponse.text();
          console.error(`Failed to auto-deliver order ${orderId} (attempt ${attempt}): ${deliveryResponse.status} - ${errorText}`);
          
          // Retry if we haven't exceeded max attempts
          if (attempt < maxAttempts) {
            console.log(`Retrying delivery for order ${orderId} in 3 seconds...`);
            scheduleAutomaticDelivery(orderId, attempt + 1, maxAttempts);
          } else {
            console.error(`Max retry attempts reached for order ${orderId}. Order may need manual delivery.`);
            // Refresh orders to show current status
            await loadOrders();
          }
        }
      } catch (error) {
        console.error(`Error auto-delivering order ${orderId} (attempt ${attempt}):`, error);
        
        // Retry if we haven't exceeded max attempts
        if (attempt < maxAttempts) {
          console.log(`Retrying delivery for order ${orderId} in 3 seconds...`);
          scheduleAutomaticDelivery(orderId, attempt + 1, maxAttempts);
        } else {
          console.error(`Max retry attempts reached for order ${orderId}. Order may need manual delivery.`);
          // Refresh orders to show current status
          await loadOrders();
        }
      }
    }, delay);
  }

  // Order delivery
  async function deliverOrder(orderId) {
    if (!confirm('Mark this order as delivered?')) {
      return;
    }

    try {
      const response = await fetch(`/api/orders/${orderId}/deliver`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({})
      });

      if (!response.ok) {
        throw new Error(`Failed to deliver order: ${response.status}`);
      }

      showModal('Success', 'Order marked as delivered');
      await loadOrders();
    } catch (error) {
      showModal('Error', `Failed to deliver order: ${error.message}`);
    }
  }

  // Order confirmation
  async function confirmOrder(orderId) {
    if (!confirm('Confirm this order as completed?')) {
      return;
    }

    try {
      const response = await fetch(`/api/orders/${orderId}/confirm`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({})
      });

      if (!response.ok) {
        throw new Error(`Failed to confirm order: ${response.status}`);
      }

      showModal('Success', 'Order confirmed successfully');
      await loadOrders();
    } catch (error) {
      showModal('Error', `Failed to confirm order: ${error.message}`);
    }
  }

  // Order return
  async function returnOrder(orderId) {
    const reason = prompt('Please enter a return reason:');
    if (!reason) return;

    if (!confirm(`Are you sure you want to return this order?\n\nReason: ${reason}\n\nThis will process a refund and restore stock to inventory.`)) {
      return;
    }

    try {
      const response = await fetch(`/api/orders/${orderId}/return`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ reason: reason })
      });

      if (!response.ok) {
        throw new Error(`Failed to return order: ${response.status}`);
      }

      const result = await response.json();
      let message = 'Order returned successfully';
      if (result.refund_id) {
        message += `\nRefund ID: ${result.refund_id}`;
      }
      
      showModal('Success', message);
      await loadOrders();
    } catch (error) {
      showModal('Error', `Failed to return order: ${error.message}`);
    }
  }

  // Make functions globally available
  window.shipOrder = shipOrder;
  window.deliverOrder = deliverOrder;
  window.confirmOrder = confirmOrder;
  window.returnOrder = returnOrder;

  async function refundOrder(orderId) {
    if (!confirm('Refund this order?')) return;
    try {
      const response = await fetch(`/api/orders/${orderId}/refund`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({}) });
      if (!response.ok) throw new Error(`Refund failed: ${response.status}`);
      showModal('Success', 'Refund requested');
      await loadOrders();
    } catch (e) { showModal('Error', String(e)) }
  }
  window.refundOrder = refundOrder;
