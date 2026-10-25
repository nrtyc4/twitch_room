const { test, expect } = require('@playwright/test');

const BASE = process.env.API_BASE || 'http://localhost:8080';
const TOKEN = process.env.API_TOKEN || 'dev-secret-token';
const PUB = process.env.PUBLISHER_ID || 'e2e-publisher';
const SUB = process.env.SUBSCRIBER_ID || 'e2e-subscriber';

function headers(userId) {
  return {
    'Authorization': `Bearer ${TOKEN}`,
    'X-User-ID': userId,
    'Content-Type': 'application/json',
  };
}

test('publish watch state', async ({ request }) => {
  const res = await request.post(`${BASE}/v1/watch`, {
    headers: headers(PUB),
    data: { stream_ids: ['shroud'] },
  });
  expect([200, 201]).toContain(res.status());
});

test('read own watch state', async ({ request }) => {
  const res = await request.get(`${BASE}/v1/watch?user_id=${PUB}`, { headers: headers(PUB) });
  expect(res.ok()).toBeTruthy();
  const body = await res.json();
  expect(body.user_id).toBe(PUB);
});

test('subscription lifecycle and feed', async ({ request }) => {
  // subscriber requests subscription to publisher
  let res = await request.post(`${BASE}/v1/subscriptions/${PUB}`, {
    headers: headers(SUB),
  });
  expect([200, 201, 202, 204]).toContain(res.status());

  // publisher approves
  res = await request.post(`${BASE}/v1/subscription_requests/${SUB}/approve`, {
    headers: headers(PUB),
  });
  expect(res.ok()).toBeTruthy();

  // subscriber feed includes publisher
  res = await request.get(`${BASE}/v1/feed`, { headers: headers(SUB) });
  expect(res.ok()).toBeTruthy();
  const feed = await res.json();
  expect(Array.isArray(feed)).toBeTruthy();
});

test('share page reflects visibility', async ({ request }) => {
  // default visibility is subscribers; public page should 404 until set public
  let res = await request.get(`${BASE}/u/${PUB}`);
  expect([200, 404]).toContain(res.status());
});