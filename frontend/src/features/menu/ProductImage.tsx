import { useState } from 'react'

import { CupIcon } from '@/components/icons'
import { cn } from '@/lib/utils'

/** Product photo with a warm placeholder when there is no image or it fails to load. */
export function ProductImage({ src, alt, className }: { src: string | null; alt: string; className?: string }) {
  const [failed, setFailed] = useState(false)
  if (!src || failed) {
    return (
      <div
        className={cn('grid place-items-center bg-gradient-to-br from-muted via-[#ead9c5] to-accent/40 text-primary/50', className)}
        aria-hidden
      >
        <CupIcon width={40} height={40} strokeWidth={1.4} />
      </div>
    )
  }
  return (
    <img
      src={src}
      alt={alt}
      loading="lazy"
      decoding="async"
      onError={() => setFailed(true)}
      className={cn('bg-muted object-cover', className)}
    />
  )
}
