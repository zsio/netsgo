if (typeof globalThis.navigator !== 'undefined') {
  Object.defineProperty(globalThis.navigator, 'language', {
    value: 'en-US',
    configurable: true,
  });
  Object.defineProperty(globalThis.navigator, 'languages', {
    value: ['en-US'],
    configurable: true,
  });
}
