// load-tests/helpers/payload.js
// Utilities for generating random UUIDs, 15-minute aligned pickup timestamps, and order payloads.

export function generateUUID() {
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function (c) {
    const r = (Math.random() * 16) | 0;
    const v = c === 'x' ? r : (r & 0x3) | 0x8;
    return v.toString(16);
  });
}

export function getAlignedPickupTime(hoursInFuture = 2) {
  const d = new Date(Date.now() + hoursInFuture * 3600 * 1000);
  const minutes = d.getUTCMinutes();
  const remainder = minutes % 15;
  d.setUTCMinutes(minutes - remainder);
  d.setUTCSeconds(0);
  d.setUTCMilliseconds(0);
  return d.toISOString();
}

export function buildOrderPayload(flavorId, portions = 1, pickupAt = null) {
  return {
    pickup_at: pickupAt || getAlignedPickupTime(2),
    items: [
      {
        flavor_id: flavorId || '00000000-0000-0000-0000-000000000001',
        portions: portions,
      },
    ],
  };
}
