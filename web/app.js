let PRODUCTS = []; // Will be loaded dynamically
let currentMerchantId = 'm_001'; // Default merchant
  
  const basket = new Map(); // productId -> quantity
  
  const $ = (sel) => document.querySelector(sel);
  const productsEl = $('#products');
  const basketEmptyEl = $('#basket-empty');
  const basketTableEl = $('#basket-table');
  const basketBodyEl = $('#basket-body');
  const checkoutBtn = $('#checkout-btn');
  const customerInput = $('#customer-id');
  const merchantSelect = $('#merchant-select');
  const refreshProductsBtn = $('#refresh-products');
  
  const ordersListEl = document.getElementById('orders-list');

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
  
  function addToBasket(pid) {
    basket.set(pid, (basket.get(pid) || 0) + 1);
    renderBasket();
  }
  function changeQty(pid, delta) {
    const next = (basket.get(pid) || 0) + delta;
    if (next <= 0) basket.delete(pid);
    else basket.set(pid, next);
    renderBasket();
  }
  function removeFromBasket(pid) {
    basket.delete(pid);
    renderBasket();
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
    renderBasket();
  };
  
  refreshProductsBtn.onclick = () => {
    loadProducts();
  };
  
  // Load products and initialize
  loadProducts();
  renderBasket();
  loadOrders();

  async function loadOrders() {
    try {
      const res = await fetch('/api/orders');
      if (!res.ok) return;
      const data = await res.json();
      const orders = data.orders || [];
      ordersListEl.innerHTML = '';
      orders.forEach(o => {
        const div = document.createElement('div');
        div.className = 'order-card';
        const items = (o.items || []).map(it => `${(it.name || it.product_id)} x${it.quantity}`).join(', ');
        const inv = o.invoice_url ? `<a href="${o.invoice_url}" target="_blank" rel="noopener">invoice</a>` : '-';
        div.innerHTML = `
          <div><b>ID</b>: <code>${o.id}</code></div>
          <div><b>Status</b>: ${o.status} | <b>Payment</b>: ${o.payment_status || '-'}</div>
          <div><b>Items</b>: ${items || '-'}</div>
          <div><b>Invoice</b>: ${inv}</div>
          <div class="order-actions">
            ${o.status === 'PROCESSING' ? 
              `<button onclick="shipOrder('${o.id}')" style="background-color: #007bff; color: white; border: none; padding: 4px 8px; border-radius: 4px; cursor: pointer; margin-right: 8px;">Ship Order (Auto-Deliver)</button>` : ''}
            ${o.status === 'SHIPPED' ? 
              `<span style="color: #28a745; font-weight: bold; margin-right: 8px;">In Transit...</span>` : ''}
            ${o.status === 'PROCESSING' || o.status === 'SHIPPED' ? 
              `<button onclick="cancelOrder('${o.id}')" style="background-color: #dc3545; color: white; border: none; padding: 4px 8px; border-radius: 4px; cursor: pointer;">Cancel Order</button>` : ''}
            ${o.status === 'DELIVERED' ? 
              `<button onclick="confirmOrder('${o.id}')" style="background-color: #28a745; color: white; border: none; padding: 4px 8px; border-radius: 4px; cursor: pointer; margin-right: 8px;">Confirm Order</button>
               <button onclick="returnOrder('${o.id}')" style="background-color: #dc3545; color: white; border: none; padding: 4px 8px; border-radius: 4px; cursor: pointer;">Return Order</button>` : ''}
            ${o.status === 'COMPLETED' ? 
              `<span style="color: #28a745; font-weight: bold;">✓ Order Completed</span>` : ''}
            ${o.status === 'RETURNED' ? 
              `<span style="color: #dc3545; font-weight: bold;">↩ Order Returned</span>` : ''}
          </div>
        `;
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

      showModal('Shipping Started', 'Order is being shipped and will be automatically delivered in 3 seconds.');
      await loadOrders();
      
      // Schedule automatic delivery after 3 seconds
      setTimeout(async () => {
        try {
          const deliveryResponse = await fetch(`/api/orders/${orderId}/deliver`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({})
          });
          
          if (deliveryResponse.ok) {
            console.log(`Order ${orderId} automatically delivered`);
            await loadOrders(); // Refresh the order list to show the updated status
          } else {
            console.error(`Failed to auto-deliver order ${orderId}`);
          }
        } catch (error) {
          console.error(`Error auto-delivering order ${orderId}:`, error);
        }
      }, 3000); // 3 seconds
    } catch (error) {
      showModal('Error', `Failed to ship order: ${error.message}`);
    }
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