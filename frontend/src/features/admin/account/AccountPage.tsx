import { useMutation } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { isApiError } from '@/lib/api'
import { faDigits } from '@/lib/digits'

import { authApi } from '../api'
import { useAuth, useToast } from '../hooks'
import { PushCard } from '../push/PushCard'
import { roleLabel } from '../labels'
import { Card, Field, FormError, PageHeader, inputClass } from '../ui'

const MIN_PASSWORD = 10

export function AccountPage() {
  const { user, logout, can } = useAuth()
  const toast = useToast()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [repeat, setRepeat] = useState('')
  const mismatch = repeat !== '' && next !== repeat
  const invalid = !current || next.length < MIN_PASSWORD || next !== repeat
  const change = useMutation({
    mutationFn: () => authApi.changePassword(current, next),
    onSuccess: () => {
      toast('رمز عبور تغییر کرد. نشست‌های دیگر شما بسته شد.')
      setCurrent('')
      setNext('')
      setRepeat('')
    },
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!invalid) change.mutate()
  }

  return (
    <div className="max-w-lg space-y-4">
      <PageHeader title="حساب کاربری" />
      <Card>
        <p className="font-bold">{user.display_name}</p>
        <p className="text-sm text-muted-foreground">
          <span dir="ltr">{user.username}</span> · {roleLabel[user.role]}
        </p>
        <Button variant="outline" className="mt-3" onClick={logout}>
          خروج از حساب
        </Button>
      </Card>
      {can('push.manage') && <PushCard />}
      <Card>
        <h2 className="mb-3 font-bold">تغییر رمز عبور</h2>
        <form onSubmit={submit} className="space-y-4" noValidate>
          <Field label="رمز فعلی">
            {(p) => (
              <input {...p} dir="ltr" type="password" autoComplete="current-password" className={inputClass} value={current} onChange={(e) => setCurrent(e.target.value)} />
            )}
          </Field>
          <Field label="رمز جدید" hint={`حداقل ${faDigits(MIN_PASSWORD)} نویسه.`}>
            {(p) => <input {...p} dir="ltr" type="password" autoComplete="new-password" className={inputClass} value={next} onChange={(e) => setNext(e.target.value)} />}
          </Field>
          <Field label="تکرار رمز جدید" error={mismatch ? 'با رمز جدید یکسان نیست.' : undefined}>
            {(p) => <input {...p} dir="ltr" type="password" autoComplete="new-password" className={inputClass} value={repeat} onChange={(e) => setRepeat(e.target.value)} />}
          </Field>
          {isApiError(change.error, 'VALIDATION_ERROR') && (change.error.details.fields as Record<string, string> | undefined)?.current_password ? (
            <p role="alert" className="rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger">
              رمز فعلی اشتباه است.
            </p>
          ) : (
            <FormError error={change.error} />
          )}
          <Button type="submit" disabled={invalid || change.isPending}>
            {change.isPending ? 'در حال ثبت…' : 'تغییر رمز'}
          </Button>
        </form>
      </Card>
    </div>
  )
}
