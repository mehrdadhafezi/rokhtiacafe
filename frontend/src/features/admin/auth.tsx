import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState, type FormEvent, type ReactNode } from 'react'

import { EmptyState, ErrorState, LoadingState } from '@/components/states'
import { Button } from '@/components/ui/button'
import { isApiError, setCsrfToken } from '@/lib/api'
import { faDigits } from '@/lib/digits'
import { errorMessageFa } from '@/lib/errors-fa'

import { authApi, type Session } from './api'
import { AuthContext, useAuth } from './hooks'
import { can, type Action } from './permissions'
import { Field, inputClass } from './ui'

const meKey = ['auth', 'me'] as const


/** `null` means "no session" (401), which shows the login screen. */
async function fetchMe(signal?: AbortSignal): Promise<Session | null> {
  try {
    return await authApi.me(signal)
  } catch (err) {
    if (isApiError(err, 'UNAUTHENTICATED')) return null
    throw err
  }
}

/**
 * Loads the session once, keeps the CSRF token in the API client and drops back to the login
 * screen whenever any request answers 401 (expired or revoked session).
 */
export function AuthProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const me = useQuery({ queryKey: meKey, queryFn: ({ signal }) => fetchMe(signal), staleTime: Infinity, retry: 1 })

  useEffect(() => setCsrfToken(me.data?.csrf_token ?? null), [me.data])

  useEffect(() => {
    const onError = (err: unknown) => {
      if (isApiError(err, 'UNAUTHENTICATED') || isApiError(err, 'CSRF_FAILED')) queryClient.setQueryData(meKey, null)
    }
    const unQueries = queryClient.getQueryCache().subscribe((e) => {
      if (e.type === 'updated' && e.action.type === 'error' && e.query.queryKey[0] !== 'auth') onError(e.action.error)
    })
    const unMutations = queryClient.getMutationCache().subscribe((e) => {
      if (e.type === 'updated' && e.action.type === 'error') onError(e.action.error)
    })
    return () => {
      unQueries()
      unMutations()
    }
  }, [queryClient])

  const logout = useMutation({
    mutationFn: authApi.logout,
    onSettled: () => {
      setCsrfToken(null)
      queryClient.clear()
      queryClient.setQueryData(meKey, null)
    },
  })

  if (me.isPending) return <LoadingState className="min-h-dvh justify-center" />
  if (me.isError) return <ErrorState error={me.error} className="m-4" onRetry={() => void me.refetch()} />
  if (!me.data) return <LoginPage onLoggedIn={(s) => queryClient.setQueryData(meKey, s)} />

  const user = me.data.user
  return (
    <AuthContext.Provider value={{ user, can: (a) => can(user.role, a), logout: () => logout.mutate() }}>
      {children}
    </AuthContext.Provider>
  )
}

function LoginPage({ onLoggedIn }: { onLoggedIn: (s: Session) => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const login = useMutation({
    mutationFn: () => authApi.login(username.trim(), password),
    onSuccess: (s) => {
      setCsrfToken(s.csrf_token)
      onLoggedIn(s)
    },
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (username.trim() && password) login.mutate()
  }
  const err = login.error
  const message = isApiError(err, 'UNAUTHENTICATED')
    ? 'نام کاربری یا رمز عبور اشتباه است.'
    : isApiError(err, 'RATE_LIMITED') && err.retryAfter
      ? `تلاش‌های ناموفق زیاد بود. ${faDigits(Math.ceil(err.retryAfter / 60))} دقیقهٔ دیگر دوباره تلاش کنید.`
      : err
        ? errorMessageFa(err)
        : null

  return (
    <main className="flex min-h-dvh items-center justify-center bg-background px-4 py-10">
      <form onSubmit={submit} className="w-full max-w-sm space-y-5 rounded-2xl border border-border/70 bg-surface p-6 shadow-sm" noValidate>
        <div className="text-center">
          <span className="mx-auto mb-3 grid size-14 place-items-center rounded-full bg-primary text-2xl text-primary-foreground" aria-hidden>
            ☕
          </span>
          <h1 className="text-2xl font-extrabold">ورود کارکنان</h1>
          <p className="mt-1 text-sm text-muted-foreground">پنل مدیریت کافه رخ تیا</p>
        </div>
        <Field label="نام کاربری">
          {(p) => (
            <input
              {...p}
              dir="ltr"
              autoComplete="username"
              autoCapitalize="none"
              spellCheck={false}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              className={inputClass}
            />
          )}
        </Field>
        <Field label="رمز عبور">
          {(p) => (
            <input
              {...p}
              dir="ltr"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className={inputClass}
            />
          )}
        </Field>
        {message && (
          <p role="alert" className="rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger">
            {message}
          </p>
        )}
        <Button type="submit" size="lg" className="w-full" disabled={login.isPending || !username.trim() || !password}>
          {login.isPending ? 'در حال ورود…' : 'ورود'}
        </Button>
      </form>
    </main>
  )
}

/** Hides a screen from roles that may not use it (the server refuses the calls anyway). */
export function RequirePermission({ action, children }: { action: Action; children: ReactNode }) {
  const { can: allowed } = useAuth()
  if (!allowed(action)) {
    return <EmptyState title="دسترسی ندارید" description="این بخش فقط برای مدیر در دسترس است." />
  }
  return <>{children}</>
}
