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
        <button ${p.quantity <= 0 ? 'disabled' : ''}>Add to basket</button>
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
      checkoutBtn.disabled = true;
    } else {
      basketEmptyEl.classList.add('hidden');
      basketTableEl.classList.remove('hidden');
      checkoutBtn.disabled = false;
    }
  
    basketBodyEl.innerHTML = '';
    items.forEach(([pid, qty]) => {
      const p = PRODUCTS.find(pp => pp.itemId === pid);
      if (!p) return; // Skip if product not found
      const tr = document.createElement('tr');
      tr.innerHTML = `
        <td>${p.name}</td>
        <td>
          <button class="qty" data-delta="-1">-</button>
          <span class="q">${qty}</span>
          <button class="qty" data-delta="1">+</button>
        </td>
        <td><button class="remove">Remove</button></td>
      `;
      tr.querySelectorAll('.qty').forEach(btn => {
        btn.onclick = () => changeQty(pid, parseInt(btn.dataset.delta, 10));
      });
      tr.querySelector('.remove').onclick = () => removeFromBasket(pid);
      basketBodyEl.appendChild(tr);
    });
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
        try { const j = await res.json(); detail = j.detail ? `<pre>${JSON.stringify(j.detail, null, 2)}</pre>` : ''; } catch {}
        showModal('Checkout failed', `Make sure the Restate runtime is active & deployment is registered.${detail}`);
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
        const confirmBtn = (o.payment_status || '').toUpperCase() === 'PAYMENT_PENDING'
          ? `<button class="confirm-paid" data-order="${o.id}">Confirm Payment</button>`
          : '';
        div.innerHTML = `
          <div><b>ID</b>: <code>${o.id}</code></div>
          <div><b>Status</b>: ${o.status} | <b>Payment</b>: ${o.payment_status || '-'}</div>
          <div><b>Items</b>: ${items || '-'}</div>
          <div><b>Invoice</b>: ${inv} ${confirmBtn}</div>
        `;
        ordersListEl.appendChild(div);
      });
      // Wire confirm buttons
      document.querySelectorAll('.confirm-paid').forEach(btn => {
        btn.addEventListener('click', async (e) => {
          const orderId = e.target.getAttribute('data-order');
          try {
            const res = await fetch(`/api/orders/${orderId}/simulate_payment_success`, { method: 'POST' });
            if (!res.ok) {
              let detail = '';
              try { const j = await res.json(); detail = j.detail || j.error || JSON.stringify(j); } catch { try { detail = await res.text(); } catch {} }
              throw new Error(detail || `HTTP ${res.status}`);
            }
            showModal('Payment simulated', `Order <code>${orderId}</code> will continue to shipping.`);
            await loadOrders();
          } catch (err) {
            showModal('Error', String(err));
          }
        });
      });
    } catch {}
  }

  // No per-order resume needed; orders list shows all