import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'

import { Dialog } from '@/components/Dialog'
import { EmptyState, ErrorState, LoadingState } from '@/components/states'
import { Button } from '@/components/ui/button'
import { isApiError } from '@/lib/api'
import { formatDateTime } from '@/lib/date'
import { faDigits } from '@/lib/digits'

import { usersApi, type AdminUser } from '../api'
import { useAuth, useToast } from '../hooks'
import { usersKey } from '../keys'
import { roleLabel, type RoleName } from '../labels'
import { Badge, Card, Field, FormError, PageHeader, Select, Switch, inputClass } from '../ui'

const MIN_PASSWORD = 10

function UserDialog({ user, onClose }: { user: AdminUser | null; onClose: () => void }) {
  const [username, setUsername] = useState('')
  const [displayName, setDisplayName] = useState(user?.display_name ?? '')
  const [role, setRole] = useState<RoleName>(user?.role ?? 'OPERATOR')
  const [active, setActive] = useState(user?.is_active ?? true)
  const [password, setPassword] = useState('')
  const queryClient = useQueryClient()
  const toast = useToast()
  const usernameOk = /^[a-z0-9_.]{3,32}$/.test(username.trim().toLowerCase())
  const invalid = !displayName.trim() || (!user && (!usernameOk || password.length < MIN_PASSWORD))

  const save = useMutation({
    mutationFn: () =>
      user
        ? usersApi.update(user.id, { display_name: displayName.trim(), role, is_active: active })
        : usersApi.create({ username: username.trim().toLowerCase(), display_name: displayName.trim(), role, password }),
    onSuccess: (u) => {
      void queryClient.invalidateQueries({ queryKey: usersKey })
      toast(user ? `${u.display_name} ذخیره شد.` : `کاربر ${u.username} ساخته شد.`)
      onClose()
    },
  })
  const conflict = isApiError(save.error, 'CONFLICT')

  return (
    <Dialog
      open
      onClose={onClose}
      title={user ? `ویرایش ${user.display_name}` : 'کاربر جدید'}
      footer={
        <>
          <Button className="flex-1" disabled={invalid || save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? 'در حال ذخیره…' : 'ذخیره'}
          </Button>
          <Button variant="outline" className="flex-1" onClick={onClose}>
            انصراف
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {!user && (
          <Field
            label="نام کاربری"
            hint="۳ تا ۳۲ حرف انگلیسی کوچک، عدد، نقطه یا زیرخط."
            error={username && !usernameOk ? 'نام کاربری معتبر نیست.' : undefined}
          >
            {(p) => (
              <input
                {...p}
                dir="ltr"
                autoCapitalize="none"
                spellCheck={false}
                className={inputClass}
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
            )}
          </Field>
        )}
        <Field label="نام نمایشی">
          {(p) => <input {...p} className={inputClass} maxLength={60} value={displayName} onChange={(e) => setDisplayName(e.target.value)} />}
        </Field>
        <Field label="نقش" hint="کارمند: سفارش‌ها، دریافت پول و موجود/تمام‌شدن محصولات. مدیر: همهٔ بخش‌ها.">
          {(p) => (
            <Select {...p} value={role} onChange={(e) => setRole(e.target.value as RoleName)}>
              <option value="OPERATOR">{roleLabel.OPERATOR}</option>
              <option value="ADMIN">{roleLabel.ADMIN}</option>
            </Select>
          )}
        </Field>
        {!user && (
          <Field label="رمز عبور" hint={`حداقل ${faDigits(MIN_PASSWORD)} نویسه.`}>
            {(p) => (
              <input
                {...p}
                dir="ltr"
                type="password"
                autoComplete="new-password"
                className={inputClass}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            )}
          </Field>
        )}
        {user && (
          <div className="flex items-center justify-between rounded-lg border px-3">
            <span className="font-medium">حساب فعال است</span>
            <Switch label="حساب فعال است" checked={active} onChange={setActive} />
          </div>
        )}
        {conflict ? (
          <p role="alert" className="rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger">
            {user ? 'باید دست‌کم یک مدیر فعال باقی بماند.' : 'این نام کاربری قبلاً استفاده شده است.'}
          </p>
        ) : (
          <FormError error={save.error} />
        )}
      </div>
    </Dialog>
  )
}

function ResetPasswordDialog({ user, onClose }: { user: AdminUser; onClose: () => void }) {
  const [password, setPassword] = useState('')
  const toast = useToast()
  const reset = useMutation({
    mutationFn: () => usersApi.resetPassword(user.id, password),
    onSuccess: () => {
      toast(`رمز ${user.display_name} تغییر کرد و نشست‌های او بسته شد.`)
      onClose()
    },
  })
  return (
    <Dialog
      open
      onClose={onClose}
      title={`تعیین رمز جدید برای ${user.display_name}`}
      footer={
        <>
          <Button className="flex-1" disabled={password.length < MIN_PASSWORD || reset.isPending} onClick={() => reset.mutate()}>
            تغییر رمز
          </Button>
          <Button variant="outline" className="flex-1" onClick={onClose}>
            انصراف
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <Field label="رمز جدید" hint={`حداقل ${faDigits(MIN_PASSWORD)} نویسه. کاربر از همهٔ دستگاه‌ها خارج می‌شود.`}>
          {(p) => (
            <input
              {...p}
              dir="ltr"
              type="password"
              autoComplete="new-password"
              className={inputClass}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          )}
        </Field>
        <FormError error={reset.error} />
      </div>
    </Dialog>
  )
}

export function UsersPage() {
  const { user: me } = useAuth()
  const q = useQuery({ queryKey: usersKey, queryFn: ({ signal }) => usersApi.list(signal) })
  const [editing, setEditing] = useState<AdminUser | 'new' | null>(null)
  const [resetting, setResetting] = useState<AdminUser | null>(null)

  if (q.isPending) return <LoadingState />
  if (q.isError) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />

  return (
    <div className="space-y-4">
      <PageHeader title="کاربران" description="حساب‌های کارکنان و نقش آن‌ها" actions={<Button onClick={() => setEditing('new')}>کاربر جدید</Button>} />
      {q.data.items.length === 0 ? (
        <EmptyState title="کاربری نیست" />
      ) : (
        <ul className="grid gap-3 md:grid-cols-2">
          {q.data.items.map((u) => (
            <li key={u.id}>
              <Card className={u.is_active ? undefined : 'opacity-70'}>
                <div className="flex items-start justify-between gap-2">
                  <div>
                    <p className="font-bold">
                      {u.display_name} {u.id === me.id && <span className="text-sm font-normal text-muted-foreground">(شما)</span>}
                    </p>
                    <p className="text-sm text-muted-foreground" dir="ltr">
                      {u.username}
                    </p>
                  </div>
                  <div className="flex flex-wrap justify-end gap-1">
                    <Badge tone={u.role === 'ADMIN' ? 'primary' : 'neutral'}>{roleLabel[u.role]}</Badge>
                    {!u.is_active && <Badge tone="danger">غیرفعال</Badge>}
                  </div>
                </div>
                <p className="mt-2 text-sm text-muted-foreground">
                  آخرین ورود: {u.last_login_at ? formatDateTime(u.last_login_at) : 'هنوز وارد نشده'}
                </p>
                <div className="mt-3 flex gap-2">
                  <Button variant="outline" className="flex-1" onClick={() => setEditing(u)}>
                    ویرایش
                  </Button>
                  <Button variant="ghost" className="flex-1" onClick={() => setResetting(u)}>
                    تعیین رمز
                  </Button>
                </div>
              </Card>
            </li>
          ))}
        </ul>
      )}
      {editing && <UserDialog user={editing === 'new' ? null : editing} onClose={() => setEditing(null)} />}
      {resetting && <ResetPasswordDialog user={resetting} onClose={() => setResetting(null)} />}
    </div>
  )
}
