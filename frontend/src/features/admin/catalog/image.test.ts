import { describe, expect, it } from 'vitest'

import { MAX_IMAGE_BYTES, imageFileProblem } from './image'

describe('product image pre-upload check', () => {
  it('accepts JPEG, PNG and WebP up to 3 MB', () => {
    expect(imageFileProblem({ type: 'image/jpeg', size: 1000 })).toBeNull()
    expect(imageFileProblem({ type: 'image/webp', size: MAX_IMAGE_BYTES })).toBeNull()
  })

  it('rejects other types and files over 3 MB with Persian messages', () => {
    expect(imageFileProblem({ type: 'text/plain', size: 10 })).toContain('JPEG')
    expect(imageFileProblem({ type: 'image/gif', size: 10 })).toContain('JPEG')
    expect(imageFileProblem({ type: 'image/png', size: MAX_IMAGE_BYTES + 1 })).toBe('حجم تصویر بیشتر از ۳ مگابایت است.')
  })
})
