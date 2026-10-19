import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/states'

export function NotFoundPage({ homeHref }: { homeHref: string }) {
  return (
    <main className="mx-auto max-w-lg p-4">
      <EmptyState
        title="صفحه پیدا نشد"
        description="نشانی وارد شده وجود ندارد."
        action={
          <Button asChild variant="outline">
            <a href={homeHref}>بازگشت</a>
          </Button>
        }
      />
    </main>
  )
}
