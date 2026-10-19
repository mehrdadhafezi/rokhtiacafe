/** Product image limits enforced by the server (API §5.4); checked before uploading. */
export const MAX_IMAGE_BYTES = 3 * 1024 * 1024
export const IMAGE_TYPES = ['image/jpeg', 'image/png', 'image/webp'] as const

/**
 * Persian reason a file cannot be uploaded, or null when it may be sent. Checking locally gives
 * the right message even when a proxy turns the server's early 413 into a 502, and spares phones
 * from sending megabytes that would be rejected anyway.
 */
export function imageFileProblem(file: Pick<File, 'type' | 'size'>): string | null {
  if (!(IMAGE_TYPES as readonly string[]).includes(file.type)) return 'فقط تصویرهای JPEG، PNG یا WebP پذیرفته می‌شوند.'
  if (file.size > MAX_IMAGE_BYTES) return 'حجم تصویر بیشتر از ۳ مگابایت است.'
  return null
}
