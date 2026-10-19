import { useEffect } from 'react'

import { pushApi } from '../api'
import { useAuth } from '../hooks'
import { browserEnv, currentSubscription, detectSupport, subscriptionBody } from './push'

/**
 * Once per app start, re-register this device's existing subscription (an idempotent upsert),
 * so a subscription the server dropped or the browser rotated is restored without the admin
 * having to notice. Never prompts for permission.
 */
export function usePushSync() {
  const { can } = useAuth()
  const allowed = can('push.manage')
  useEffect(() => {
    if (!allowed || detectSupport(browserEnv()) !== 'supported' || Notification.permission !== 'granted') return
    currentSubscription()
      .then((sub) => (sub ? pushApi.subscribe(subscriptionBody(sub.toJSON())) : undefined))
      .catch(() => {
        /* best effort: the account page shows the real state */
      })
  }, [allowed])
}
