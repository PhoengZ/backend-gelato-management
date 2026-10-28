// load-tests/helpers/auth.js
// Utility to mint valid HS256 JWT tokens for testing authentication under load.

import crypto from 'k6/crypto';
import encoding from 'k6/encoding';
import { CONFIG } from '../config/options.js';
import { generateUUID } from './payload.js';

export function mintCustomerToken(userId, role = 'CUSTOMER', secret = CONFIG.JWT_SECRET) {
  const header = { alg: 'HS256', typ: 'JWT' };
  const now = Math.floor(Date.now() / 1000);
  // GelatoFlow API Gateway enforces that claims.Subject (sub) must be a valid UUID.
  const uid = userId && userId.includes('-') ? userId : generateUUID();

  const payload = {
    sub: uid,
    role: role,
    iss: CONFIG.JWT_ISSUER,
    aud: CONFIG.JWT_AUDIENCE,
    iat: now,
    exp: now + 3600,
  };

  const headerB64 = encoding.b64encode(JSON.stringify(header), 'rawurl');
  const payloadB64 = encoding.b64encode(JSON.stringify(payload), 'rawurl');
  const unsignedToken = `${headerB64}.${payloadB64}`;
  const signature = crypto.hmac('sha256', secret, unsignedToken, 'base64rawurl');

  return `${unsignedToken}.${signature}`;
}

export function authHeader(token) {
  return {
    Authorization: `Bearer ${token}`,
  };
}
