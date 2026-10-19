const { test, expect } = require('@playwright/test');

const BASE = process.env.API_BASE || 'http://localhost:8080';

test('health endpoint responds', async ({ request }) => {
  const res = await request.get(`${BASE}/health`);
  expect(res.ok()).toBeTruthy();
  const body = await res.json();
  expect(body.status).toBe('ok');
});

test('metrics endpoint responds', async ({ request }) => {
  const res = await request.get(`${BASE}/metrics`);
  // Either 200 (no token) or 401 (token configured) is acceptable.
  expect([200, 401]).toContain(res.status());
});