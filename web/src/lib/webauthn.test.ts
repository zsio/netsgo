import { describe, expect, it } from 'bun:test';
import { webAuthnEncodingForTests } from './webauthn';

describe('WebAuthn base64url helpers', () => {
  it('round-trips ArrayBuffer values', () => {
    const bytes = new Uint8Array([0, 1, 2, 253, 254, 255]);
    const encoded = webAuthnEncodingForTests.arrayBufferToBase64Url(bytes.buffer);
    expect(encoded).toBe('AAEC_f7_');

    const decoded = new Uint8Array(webAuthnEncodingForTests.base64UrlToArrayBuffer(encoded));
    expect(Array.from(decoded)).toEqual(Array.from(bytes));
  });
});
