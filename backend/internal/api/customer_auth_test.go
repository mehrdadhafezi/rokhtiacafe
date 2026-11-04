package api_test

import (
 "net/http"
 "sync"
 "testing"
)

const customerPath="/api/v1/customer/auth/"

func registerCustomer(t *testing.T,e *env,phone string)*client {
 t.Helper()
 c:=e.newClient()
 resp,body:=c.do(http.MethodPost,customerPath+"register",map[string]string{
  "first_name":"مریم","last_name":"احمدی","phone":phone,"password":"secure-cafe-2026",
 })
 expect(t,resp,body,http.StatusCreated,"")
 if resp.StatusCode!=201 {t.Fatalf("registration failed: %v",body)}
 c.csrf=body["csrf_token"].(string)
 if body["customer"].(map[string]any)["phone"]!="09123456789" {t.Fatalf("phone normalization: %v",body)}
 return c
}

func TestCustomerRegisterLoginLogoutAndStaffIsolation(t *testing.T){
 e:=newEnv(t)
 c:=registerCustomer(t,e,"+989123456789")
 resp,body:=c.do(http.MethodGet,customerPath+"me",nil)
 expect(t,resp,body,200,"")
 if body["customer"].(map[string]any)["first_name"]!="مریم"{t.Fatalf("unexpected account: %v",body)}
 resp,body=c.do(http.MethodGet,"/api/v1/auth/me",nil)
 expect(t,resp,body,401,"UNAUTHENTICATED")
 staff:=e.newClient().login("admin",adminPassword)
 resp,body=staff.do(http.MethodGet,customerPath+"me",nil)
 expect(t,resp,body,401,"UNAUTHENTICATED")

 resp,body=c.do(http.MethodPost,customerPath+"logout",nil,noCSRF)
 expect(t,resp,body,403,"CSRF_FAILED")
 resp,body=c.do(http.MethodPost,customerPath+"logout",nil,withOrigin("https://evil.example"))
 expect(t,resp,body,403,"CSRF_FAILED")
 resp,body=c.do(http.MethodPost,customerPath+"logout",nil)
 expect(t,resp,body,204,"")
 resp,body=c.do(http.MethodGet,customerPath+"me",nil)
 expect(t,resp,body,401,"UNAUTHENTICATED")

 resp,body=c.do(http.MethodPost,customerPath+"login",map[string]string{"phone":"۰۹۱۲۳۴۵۶۷۸۹","password":"secure-cafe-2026"})
 expect(t,resp,body,200,"")
 if resp.Header.Get("Cache-Control")!="no-store"{t.Fatal("auth response must be no-store")}
 c.csrf=body["csrf_token"].(string)
 resp,body=c.do(http.MethodGet,customerPath+"me",nil)
 expect(t,resp,body,200,"")
}

func TestCustomerRegistrationValidationAndDuplicate(t *testing.T){
 e:=newEnv(t)
 c:=e.newClient()
 resp,body:=c.do(http.MethodPost,customerPath+"register",map[string]string{
  "first_name":"x","last_name":"y","phone":"wrong","password":"secure-cafe-2026"})
 expect(t,resp,body,422,"VALIDATION_ERROR")
 resp,body=c.do(http.MethodPost,customerPath+"register",map[string]string{
  "first_name":"x","last_name":"y","phone":"09123456789","password":"123"})
 expect(t,resp,body,422,"VALIDATION_ERROR")
 registerCustomer(t,e,"09123456789")
 resp,body=c.do(http.MethodPost,customerPath+"register",map[string]string{
  "first_name":"x","last_name":"y","phone":"09123456789","password":"secure-cafe-2026"})
 expect(t,resp,body,409,"CONFLICT")
 resp,body=c.do(http.MethodPost,customerPath+"register",map[string]string{
  "first_name":"x","last_name":"y","phone":"09111111111","password":"secure-cafe-2026"},withOrigin("https://evil.example"))
 expect(t,resp,body,403,"CSRF_FAILED")
}

func TestCustomerLoginUniformErrorsAndPasswordRevocation(t *testing.T){
 e:=newEnv(t)
 one:=registerCustomer(t,e,"09123456789")
 two:=e.newClient()
 resp,body:=two.do(http.MethodPost,customerPath+"login",map[string]string{"phone":"09123456789","password":"wrong-password"})
 expect(t,resp,body,401,"UNAUTHENTICATED")
 msg:=body["error"].(map[string]any)["message"]
 resp,body=two.do(http.MethodPost,customerPath+"login",map[string]string{"phone":"09111111111","password":"wrong-password"})
 expect(t,resp,body,401,"UNAUTHENTICATED")
 if body["error"].(map[string]any)["message"]!=msg{t.Fatal("login reveals account existence")}
 resp,body=two.do(http.MethodPost,customerPath+"login",map[string]string{"phone":"09123456789","password":"secure-cafe-2026"})
 expect(t,resp,body,200,"")
 two.csrf=body["csrf_token"].(string)
 resp,body=one.do(http.MethodPost,customerPath+"password",map[string]string{
  "current_password":"secure-cafe-2026","new_password":"new-secure-2027"})
 expect(t,resp,body,204,"")
 resp,body=two.do(http.MethodGet,customerPath+"me",nil)
 expect(t,resp,body,401,"UNAUTHENTICATED")
 resp,body=one.do(http.MethodGet,customerPath+"me",nil)
 expect(t,resp,body,200,"")
 resp,body=e.newClient().do(http.MethodPost,customerPath+"login",map[string]string{"phone":"09123456789","password":"new-secure-2027"})
 expect(t,resp,body,200,"")
}

func TestCustomerSessionIdleExpires(t *testing.T){
 e:=newEnv(t)
 c:=registerCustomer(t,e,"09123456789")
 if _,err:=e.pool.Exec(e.ctx,"UPDATE customer_sessions SET last_seen_at=now()-interval '13 hours'");err!=nil{t.Fatal(err)}
 resp,body:=c.do(http.MethodGet,customerPath+"me",nil)
 expect(t,resp,body,401,"UNAUTHENTICATED")
}

func TestCustomerConcurrentRegistrationOneAccount(t *testing.T){
 e:=newEnv(t)
 start:=make(chan struct{})
 var wg sync.WaitGroup
 statuses:=make([]int,2)
 for i:=range statuses {
  wg.Add(1)
  go func(i int){
   defer wg.Done();<-start
   c:=e.newClient()
   resp,_:=c.do(http.MethodPost,customerPath+"register",map[string]string{
    "first_name":"مریم","last_name":"احمدی","phone":"09123456789","password":"secure-cafe-2026"})
   statuses[i]=resp.StatusCode
  }(i)
 }
 close(start);wg.Wait()
 if !((statuses[0]==201&&statuses[1]==409)||(statuses[1]==201&&statuses[0]==409)){
  t.Fatalf("concurrent registration: %v",statuses)
 }
 var n int
 if err:=e.pool.QueryRow(e.ctx,"SELECT count(*) FROM customers").Scan(&n);err!=nil||n!=1{
  t.Fatalf("account count=%d err=%v",n,err)
 }
}
