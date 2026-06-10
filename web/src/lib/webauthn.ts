function base64UrlToArrayBuffer(value: string): ArrayBuffer {
  const normalized = value.replace(/-/g, '+').replace(/_/g, '/');
  const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, '=');
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes.buffer;
}

function arrayBufferToBase64Url(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer);
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
}

function maybeCredentialDescriptors(value: unknown): PublicKeyCredentialDescriptor[] | undefined {
  if (!Array.isArray(value)) return undefined;
  return value.map((item) => {
    const descriptor = item as { type: PublicKeyCredentialType; id: string; transports?: AuthenticatorTransport[] };
    return {
      ...descriptor,
      id: base64UrlToArrayBuffer(descriptor.id),
    };
  });
}

export function normalizeCreationOptions(raw: unknown): PublicKeyCredentialCreationOptions {
  const container = raw as { publicKey?: Record<string, unknown>; response?: Record<string, unknown> };
  const options = (container.publicKey ?? container.response ?? raw) as Record<string, unknown>;
  const user = options.user as Record<string, unknown>;
  return {
    ...options,
    challenge: base64UrlToArrayBuffer(options.challenge as string),
    user: {
      ...user,
      id: typeof user.id === 'string' ? base64UrlToArrayBuffer(user.id) : user.id as BufferSource,
    },
    excludeCredentials: maybeCredentialDescriptors(options.excludeCredentials),
  } as PublicKeyCredentialCreationOptions;
}

export function normalizeRequestOptions(raw: unknown): PublicKeyCredentialRequestOptions {
  const container = raw as { publicKey?: Record<string, unknown>; response?: Record<string, unknown> };
  const options = (container.publicKey ?? container.response ?? raw) as Record<string, unknown>;
  return {
    ...options,
    challenge: base64UrlToArrayBuffer(options.challenge as string),
    allowCredentials: maybeCredentialDescriptors(options.allowCredentials),
  } as PublicKeyCredentialRequestOptions;
}

function authenticatorAttestationResponseToJSON(response: AuthenticatorAttestationResponse) {
  return {
    clientDataJSON: arrayBufferToBase64Url(response.clientDataJSON),
    attestationObject: arrayBufferToBase64Url(response.attestationObject),
    transports: typeof response.getTransports === 'function' ? response.getTransports() : undefined,
  };
}

function authenticatorAssertionResponseToJSON(response: AuthenticatorAssertionResponse) {
  return {
    clientDataJSON: arrayBufferToBase64Url(response.clientDataJSON),
    authenticatorData: arrayBufferToBase64Url(response.authenticatorData),
    signature: arrayBufferToBase64Url(response.signature),
    userHandle: response.userHandle ? arrayBufferToBase64Url(response.userHandle) : undefined,
  };
}

export function publicKeyCredentialToJSON(credential: PublicKeyCredential) {
  const response = credential.response;
  const serializedResponse = response instanceof AuthenticatorAttestationResponse
    ? authenticatorAttestationResponseToJSON(response)
    : authenticatorAssertionResponseToJSON(response as AuthenticatorAssertionResponse);

  return {
    id: credential.id,
    rawId: arrayBufferToBase64Url(credential.rawId),
    type: credential.type,
    response: serializedResponse,
    authenticatorAttachment: credential.authenticatorAttachment ?? undefined,
    clientExtensionResults: credential.getClientExtensionResults(),
  };
}

export function isPasskeySupported() {
  return typeof window !== 'undefined'
    && typeof window.PublicKeyCredential !== 'undefined'
    && typeof navigator.credentials?.create === 'function'
    && typeof navigator.credentials?.get === 'function';
}

export const webAuthnEncodingForTests = {
  base64UrlToArrayBuffer,
  arrayBufferToBase64Url,
};
