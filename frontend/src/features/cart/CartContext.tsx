import { createContext, useContext, useEffect, useReducer, type Dispatch, type ReactNode } from 'react'

import { cartReducer, parseStoredCart, type Cart, type CartAction } from './cart'
import { CART_KEY, loadCart, saveCart } from './storage'

interface CartContextValue {
  cart: Cart
  dispatch: Dispatch<CartAction>
}

const CartContext = createContext<CartContextValue | null>(null)

/** Cart kept in React state and mirrored to localStorage (and across tabs). */
export function CartProvider({ children }: { children: ReactNode }) {
  const [cart, dispatch] = useReducer(cartReducer, undefined, () => parseStoredCart(loadCart()))

  useEffect(() => {
    saveCart(JSON.stringify(cart))
  }, [cart])

  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key === CART_KEY) dispatch({ type: 'replace', cart: parseStoredCart(e.newValue) })
    }
    window.addEventListener('storage', onStorage)
    return () => window.removeEventListener('storage', onStorage)
  }, [])

  return <CartContext.Provider value={{ cart, dispatch }}>{children}</CartContext.Provider>
}

// eslint-disable-next-line react-refresh/only-export-components
export function useCart(): CartContextValue {
  const ctx = useContext(CartContext)
  if (!ctx) throw new Error('useCart must be used inside CartProvider')
  return ctx
}
