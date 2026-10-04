// Runs stripe-node against psp-sandbox in the stripe profile: the calls a
// backend makes, and the webhooks it gets, verified by the SDK.
// See compat/README.md.
import http from 'node:http';
import Stripe from 'stripe';

const base = new URL(process.env.PSP_URL);
const hookURL = process.env.HOOK_URL;
const secret = process.env.WEBHOOK_SECRET;
const [listenHost, listenPort] = (process.env.LISTEN || ':9000').split(':');

const client = (maxNetworkRetries, timeout = 30000) => new Stripe('sk_test_compat', {
  host: base.hostname,
  port: base.port,
  protocol: base.protocol.replace(':', ''),
  maxNetworkRetries,
  timeout,
});
const stripe = client(0);

// Start from an empty sandbox, so the counts below hold on reruns.
const reset = await fetch(new URL('/_sandbox/reset', base), { method: 'POST' });
if (reset.status !== 204) {
  console.log(`FAIL reset the sandbox: ${reset.status}`);
  process.exit(1);
}

// The receiver verifies webhooks with the SDK and keeps the ones that pass.
const got = [];
const rejected = [];
const server = http.createServer((req, res) => {
  const chunks = [];
  req.on('data', (c) => chunks.push(c));
  req.on('end', () => {
    try {
      const ev = stripe.webhooks.constructEvent(Buffer.concat(chunks), req.headers['stripe-signature'], secret);
      const obj = ev.data.object;
      got.push({ type: ev.type, intent: obj.object === 'payment_intent' ? obj.id : obj.payment_intent });
      res.writeHead(200).end();
    } catch (err) {
      rejected.push(err.message);
      res.writeHead(400).end();
    }
  });
});
server.listen(Number(listenPort), listenHost || undefined);

let failed = false;
async function check(name, f) {
  try {
    await f();
    console.log(`ok   ${name}`);
  } catch (err) {
    failed = true;
    console.log(`FAIL ${name}: ${err.message}`);
  }
}
function expect(cond, what) {
  if (!cond) throw new Error(what);
}
const params = (pm, metadata = {}) => ({
  amount: 1000,
  currency: 'eur',
  confirm: true,
  payment_method: pm,
  metadata: { sandbox_callback_url: hookURL, ...metadata },
});

let paid;
let declined;
await check('card payment succeeds', async () => {
  paid = await stripe.paymentIntents.create(params('pm_card_visa'));
  expect(paid.status === 'succeeded', `status ${paid.status}`);
});
await check('decline is a card error with the intent', async () => {
  try {
    await stripe.paymentIntents.create(params('pm_card_visa_chargeDeclinedInsufficientFunds'));
  } catch (err) {
    expect(err instanceof Stripe.errors.StripeCardError, `got ${err.type}`);
    expect(err.code === 'card_declined' && err.decline_code === 'insufficient_funds', `got ${err.code} ${err.decline_code}`);
    declined = err.raw.payment_intent;
    expect(declined && declined.id, 'no payment_intent in the error');
    return;
  }
  throw new Error('no error');
});
await check('retry after decline with another card', async () => {
  const pi = await stripe.paymentIntents.confirm(declined.id, { payment_method: 'pm_card_visa' });
  expect(pi.status === 'succeeded', `status ${pi.status}`);
});
await check('manual capture of part of the amount', async () => {
  let pi = await stripe.paymentIntents.create({ ...params('pm_card_visa'), capture_method: 'manual' });
  expect(pi.status === 'requires_capture', `authorize: ${pi.status}`);
  pi = await stripe.paymentIntents.capture(pi.id, { amount_to_capture: 600 });
  expect(pi.status === 'succeeded' && pi.amount_received === 600, `status ${pi.status}, received ${pi.amount_received}`);
});
await check('partial refund', async () => {
  const r = await stripe.refunds.create({ payment_intent: paid.id, amount: 300 });
  expect(r.status === 'succeeded', `status ${r.status}`);
});
await check('server error reaches a client without retries', async () => {
  try {
    await stripe.paymentIntents.create(params('pm_card_visa', { sandbox_scenario: 'server_error_then_success', order: 'no-retry' }));
  } catch (err) {
    expect(err.statusCode === 503, `status ${err.statusCode}`);
    return;
  }
  throw new Error('no error');
});
await check('SDK retry gets through server_error_then_success', async () => {
  const pi = await client(2).paymentIntents.create(params('pm_card_visa', { sandbox_scenario: 'server_error_then_success', order: 'retry' }));
  expect(pi.status === 'succeeded', `status ${pi.status}`);
});
await check('retry after a client timeout gets the stored answer', async () => {
  const pi = await client(2, 1000).paymentIntents.create(params('pm_card_visa', { sandbox_scenario: 'timeout_then_success; delay=3s', order: 'timeout' }));
  expect(pi.status === 'succeeded', `status ${pi.status}`);
});
await check('list pages through all intents', async () => {
  const seen = new Set();
  for await (const pi of stripe.paymentIntents.list({ limit: 2 })) {
    expect(!seen.has(pi.id), `${pi.id} twice`);
    seen.add(pi.id);
  }
  // paid, declined, manual, the retried one and the one after the timeout.
  expect(seen.size === 5, `${seen.size} intents, want 5`);
});
await check('webhooks verify with stripe.webhooks.constructEvent', async () => {
  const want = [
    ['payment_intent.succeeded', paid.id], ['charge.refunded', paid.id],
    ['payment_intent.payment_failed', declined.id], ['payment_intent.succeeded', declined.id],
  ];
  const deadline = Date.now() + 10000;
  for (const [type, intent] of want) {
    while (!got.some((h) => h.type === type && h.intent === intent)) {
      expect(Date.now() < deadline, `no ${type} for ${intent}`);
      await new Promise((r) => setTimeout(r, 50));
    }
  }
  expect(rejected.length === 0, `rejected: ${rejected.join('; ')}`);
});
await check('events retrieve', async () => {
  const list = await stripe.events.list({ type: 'charge.refunded', limit: 1 });
  expect(list.data.length === 1, 'no charge.refunded event');
  const ev = await stripe.events.retrieve(list.data[0].id);
  expect(ev.type === 'charge.refunded', `type ${ev.type}`);
});

// 3DS and Checkout. They come after the list check, which counts intents.
const control = async (path, body) => {
  const res = await fetch(new URL(path, base), {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  });
  expect(res.status === 200, `${path}: ${res.status} ${await res.text()}`);
};
let threeDS;
await check('3DS card stops in requires_action with a redirect', async () => {
  threeDS = await stripe.paymentIntents.create({ ...params('pm_card_threeDSecure2Required'), return_url: 'https://shop.test/return' });
  const na = threeDS.next_action;
  expect(threeDS.status === 'requires_action' && na && na.type === 'redirect_to_url' && na.redirect_to_url.url &&
    na.redirect_to_url.return_url === 'https://shop.test/return', `got ${threeDS.status} ${JSON.stringify(na)}`);
});
await check('authentication completes the payment', async () => {
  await control(`/_sandbox/payments/${threeDS.id}/authenticate`, { result: 'success' });
  const pi = await stripe.paymentIntents.retrieve(threeDS.id);
  expect(pi.status === 'succeeded', `status ${pi.status}`);
});
const sessionParams = () => ({
  mode: 'payment',
  success_url: 'https://shop.test/done?session={CHECKOUT_SESSION_ID}',
  line_items: [{ price_data: { currency: 'eur', unit_amount: 700, product_data: { name: 'Tea' } }, quantity: 2 }],
  payment_intent_data: { metadata: { sandbox_callback_url: hookURL } },
});
let session;
await check('checkout session completes when the customer pays', async () => {
  session = await stripe.checkout.sessions.create(sessionParams());
  expect(session.status === 'open' && session.url && session.amount_total === 1400, `created ${session.status} ${session.url}`);
  await control(`/_sandbox/checkout/${session.id}/pay`, { payment_method: 'pm_card_visa' });
  session = await stripe.checkout.sessions.retrieve(session.id);
  expect(session.status === 'complete' && session.payment_status === 'paid' && session.payment_intent,
    `after pay ${session.status} ${session.payment_status}`);
});
await check('checkout session expires', async () => {
  const cs = await stripe.checkout.sessions.create(sessionParams());
  const expired = await stripe.checkout.sessions.expire(cs.id);
  expect(expired.status === 'expired', `status ${expired.status}`);
});
await check('3DS and checkout webhooks verify', async () => {
  const want = [['payment_intent.requires_action', threeDS.id], ['checkout.session.completed', session.payment_intent]];
  const deadline = Date.now() + 10000;
  for (const [type, intent] of want) {
    while (!got.some((h) => h.type === type && h.intent === intent)) {
      expect(Date.now() < deadline, `no ${type} for ${intent}`);
      await new Promise((r) => setTimeout(r, 50));
    }
  }
  expect(rejected.length === 0, `rejected: ${rejected.join('; ')}`);
});

server.close();
process.exit(failed ? 1 : 0);
