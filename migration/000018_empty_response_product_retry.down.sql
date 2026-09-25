DROP TRIGGER publication_product_retries_reject_delete
ON wb.publication_product_retries;

DROP TRIGGER publication_product_retries_protect
ON wb.publication_product_retries;

DROP FUNCTION wb.protect_publication_product_retry();

DROP TABLE wb.publication_product_retries;
