// Countries offered for a server: ISO 3166-1 alpha-2 code and the Russian name. The flag is derived from the code.
export const countries: [string, string][] = [
  ['AT', 'Австрия'], ['AU', 'Австралия'], ['AM', 'Армения'], ['AZ', 'Азербайджан'], ['BE', 'Бельгия'],
  ['BG', 'Болгария'], ['BR', 'Бразилия'], ['BY', 'Беларусь'], ['CA', 'Канада'], ['CH', 'Швейцария'],
  ['CY', 'Кипр'], ['CZ', 'Чехия'], ['DE', 'Германия'], ['DK', 'Дания'], ['EE', 'Эстония'],
  ['ES', 'Испания'], ['FI', 'Финляндия'], ['FR', 'Франция'], ['GB', 'Великобритания'], ['GE', 'Грузия'],
  ['GR', 'Греция'], ['HK', 'Гонконг'], ['HU', 'Венгрия'], ['IE', 'Ирландия'], ['IL', 'Израиль'],
  ['IN', 'Индия'], ['IS', 'Исландия'], ['IT', 'Италия'], ['JP', 'Япония'], ['KG', 'Киргизия'],
  ['KR', 'Южная Корея'], ['KZ', 'Казахстан'], ['LT', 'Литва'], ['LU', 'Люксембург'], ['LV', 'Латвия'],
  ['MD', 'Молдова'], ['NL', 'Нидерланды'], ['NO', 'Норвегия'], ['PL', 'Польша'], ['PT', 'Португалия'],
  ['RO', 'Румыния'], ['RS', 'Сербия'], ['RU', 'Россия'], ['SE', 'Швеция'], ['SG', 'Сингапур'],
  ['SK', 'Словакия'], ['TR', 'Турция'], ['UA', 'Украина'], ['AE', 'ОАЭ'], ['US', 'США'], ['UZ', 'Узбекистан'],
]

// flagOf turns a country code into its flag emoji ("DE" -> "🇩🇪"); an unknown code gives an empty string.
export function flagOf(code: string): string {
  const c = code.trim().toUpperCase()
  if (!/^[A-Z]{2}$/.test(c)) return ''
  return String.fromCodePoint(0x1f1e6 + c.charCodeAt(0) - 65, 0x1f1e6 + c.charCodeAt(1) - 65)
}
