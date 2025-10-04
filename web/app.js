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
  
  const orderSection = $('#order-section');
  const orderIdEl = $('#order-id');
  const orderStatusEl = $('#order-status');
  const orderTrackingEl = $('#order-tracking');
  const orderJsonEl = $('#order-json');

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
        card.querySelector('button').onclick = () => addToBasket(p.item_id);
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
      const p = PRODUCTS.find(pp => pp.item_id === pid);
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
  
      const { order_id } = await res.json();
      orderSection.classList.remove('hidden');
      orderIdEl.textContent = order_id;
      basket.clear();
      renderBasket();
  
      // Update popup ke sukses + info order id
      showModal('Order processed', `Invocation success.<br/>Order ID: <code>${order_id}</code><br/>Status akan otomatis dipantau.`);
      pollOrder(order_id);
    } catch (e) {
      showModal('Network error', String(e));
    } finally {
      checkoutBtn.disabled = false;
    }
  }
  
  async function pollOrder(orderId) {
    let lastStatus = '';
    const update = async () => {
      const res = await fetch(`/api/orders/${orderId}`);
      if (!res.ok) return setTimeout(update, 2000);
      const data = await res.json();
      const order = data.order || {};
      orderStatusEl.textContent = order.status || 'UNKNOWN';
      orderTrackingEl.textContent = order.tracking_number || '-';
      orderJsonEl.textContent = JSON.stringify(data, null, 2);
  
      if (order.status && order.status !== lastStatus) {
        lastStatus = order.status;
        if (lastStatus === 'DELIVERED') {
          showModal('Order delivered', `Status: <b>DELIVERED</b><br/>Tracking: <code>${order.tracking_number || '-'}</code>`);
        } else if (lastStatus === 'CANCELLED') {
          showModal('Order cancelled', 'Status: <b>CANCELLED</b>');
        }
      }
  
      if (order.status !== 'DELIVERED' && order.status !== 'CANCELLED') {
        setTimeout(update, 2000);
      }
    };
    update();
  }
  
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