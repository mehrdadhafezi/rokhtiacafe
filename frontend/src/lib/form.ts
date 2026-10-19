import { zodResolver } from '@hookform/resolvers/zod'
import { useForm, type FieldValues, type UseFormProps } from 'react-hook-form'
import type { z } from 'zod'

/** React Hook Form bound to a Zod schema: the one form pattern used in both apps. */
export function useZodForm<TIn extends FieldValues, TOut extends FieldValues>(
  schema: z.ZodType<TOut, TIn>,
  options?: Omit<UseFormProps<TIn, unknown, TOut>, 'resolver'>,
) {
  return useForm<TIn, unknown, TOut>({ ...options, resolver: zodResolver(schema) })
}
