const PERSIAN = '۰۱۲۳۴۵۶۷۸۹'
const ARABIC = '٠١٢٣٤٥٦٧٨٩'

/** Replace Persian and Arabic-Indic digits with ASCII digits (for parsing user input). */
export function toLatinDigits(input: string): string {
  return input.replace(/[۰-۹٠-٩]/g, (d) => {
    const i = PERSIAN.indexOf(d)
    return String(i >= 0 ? i : ARABIC.indexOf(d))
  })
}

const faInt = new Intl.NumberFormat('fa-IR', { useGrouping: false })

/** A number with Persian digits and no grouping (counts, order numbers). */
export function faDigits(n: number): string {
  return faInt.format(n)
}
