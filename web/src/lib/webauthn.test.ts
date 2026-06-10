import { describe, expect, it } from 'bun:test';
import { normalizeCreationOptions, normalizeRequestOptions, webAuthnEncodingForTests } from './webauthn';

describe('WebAuthn base64url helpers', () => {
  it('round-trips ArrayBuffer values', () => {
    const bytes = new Uint8Array([0, 1, 2, 253, 254, 255]);
    const encoded = webAuthnEncodingForTests.arrayBufferToBase64Url(bytes.buffer);
    expect(encoded).toBe('AAEC_f7_');

    const decoded = new Uint8Array(webAuthnEncodingForTests.base64UrlToArrayBuffer(encoded));
    expect(Array.from(decoded)).toEqual(Array.from(bytes));
  });

  it('normalizes go-webauthn publicKey creation options', () => {
    const options = normalizeCreationOptions({
      publicKey: {
        challenge: 'AQID',
        rp: { id: 'localhost', name: 'NetsGo' },
        user: { id: 'BAUG', name: 'admin', displayName: 'admin' },
        pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
        excludeCredentials: [{ type: 'public-key', id: 'BwgJ' }],
      },
    });

    expect(Array.from(new Uint8Array(options.challenge as ArrayBuffer))).toEqual([1, 2, 3]);
    expect(Array.from(new Uint8Array(options.user.id as ArrayBuffer))).toEqual([4, 5, 6]);
    expect(Array.from(new Uint8Array(options.excludeCredentials?.[0].id as ArrayBuffer))).toEqual([7, 8, 9]);
  });

  it('normalizes go-webauthn publicKey request options', () => {
    const options = normalizeRequestOptions({
      publicKey: {
        challenge: 'AQID',
        rpId: 'localhost',
        allowCredentials: [{ type: 'public-key', id: 'BwgJ' }],
      },
    });

    expect(Array.from(new Uint8Array(options.challenge as ArrayBuffer))).toEqual([1, 2, 3]);
    expect(Array.from(new Uint8Array(options.allowCredentials?.[0].id as ArrayBuffer))).toEqual([7, 8, 9]);
  });
});
