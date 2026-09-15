DROP TRIGGER IF EXISTS trg_payments_audit ON payments;
DROP TRIGGER IF EXISTS trg_products_audit ON products;
DROP TRIGGER IF EXISTS trg_orders_audit ON orders;
DROP FUNCTION IF EXISTS fn_audit_log();
DROP TABLE IF EXISTS audit_log;
