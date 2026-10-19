import { describe, expect, it } from 'vitest'

import { detectSupport, subscriptionBody, urlBase64ToUint8Array, type BrowserEnv } from './push'

const full: BrowserEnv = { secure: true, serviceWorker: true, pushManager: true, notification: true, ios: false, standalone: false }

describe('push support detection', () => {
  it('needs a secure context and the Push API', () => {
    expect(detectSupport(full)).toBe('supported')
    expect(detectSupport({ ...full, secure: false })).toBe('insecure')
    expect(detectSupport({ ...full, pushManager: false })).toBe('unsupported')
    expect(detectSupport({ ...full, notification: false })).toBe('unsupported')
  })

  it('asks iPhone users to install the staff app first (iOS 16.4+ home-screen only)', () => {
    expect(detectSupport({ ...full, ios: true, standalone: false })).toBe('ios-needs-install')
    expect(detectSupport({ ...full, ios: true, standalone: true })).toBe('supported')
    // Safari in a tab may not even expose PushManager: the install hint still wins.
    expect(detectSupport({ ...full, ios: true, pushManager: false })).toBe('ios-needs-install')
  })
})

describe('push helpers', () => {
  it('decodes an unpadded base64url VAPID key', () => {
    const bytes = urlBase64ToUint8Array('BAEC_-8')
    expect([...bytes]).toEqual([4, 1, 2, 255, 239])
  })

  it('sends only endpoint and keys', () => {
    expect(
      subscriptionBody({ endpoint: 'https://fcm.googleapis.com/x', expirationTime: null, keys: { p256dh: 'p', auth: 'a' } }),
    ).toEqual({ endpoint: 'https://fcm.googleapis.com/x', keys: { p256dh: 'p', auth: 'a' } })
    expect(() => subscriptionBody({ endpoint: 'https://x', keys: {} })).toThrow()
  })
})
