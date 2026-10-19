import { faDigits } from '@/lib/digits'

/** «۰۸:۰۰» — the API sends HH:MM with Latin digits. */
export function faClock(hhmm: string): string {
  return hhmm.replace(/\d/g, (d) => faDigits(Number(d)))
}
