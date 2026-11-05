import { Button } from '@/components/ui/button'

import { useCustomerSession } from './session-context'

/**
 * Shown while the account state is unknown (the session lookup failed for a reason other than
 * "not signed in"). Nothing that depends on the identity — checkout, history — proceeds until a
 * retry succeeds.
 */
export function SessionCheckFailed({ what }: { what: string }) {
  const { checking, retry } = useCustomerSession()
  return (
    <div role="alert" className="space-y-2 rounded-lg bg-warning/10 px-3 py-2 text-sm text-foreground">
      <p>وضعیت ورود به حساب شما بررسی نشد (اتصال یا سرور). {what}</p>
      <Button size="sm" variant="outline" onClick={retry} disabled={checking}>
        {checking ? 'در حال بررسی…' : 'تلاش دوباره'}
      </Button>
    </div>
  )
}
