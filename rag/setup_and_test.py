#!/usr/bin/env python3
"""
Setup and test script for RAG module
Verifies OpenBao connectivity, Pinecone configuration, and performs test ingestion
"""

import sys
import os
import logging
from dotenv import load_dotenv
load_dotenv()
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(name)s - %(levelname)s - %(message)s')
logger = logging.getLogger(__name__)


def check_environment():
    """Check required environment variables"""
    logger.info("Checking environment variables...")
    
    required_vars = ['VAULT_ADDR', 'BAO_ROLE_ID', 'BAO_SECRET_ID']
    missing_vars = []
    
    for var in required_vars:
        if not os.getenv(var):
            missing_vars.append(var)
            logger.error(f"  ✗ {var} not set")
        else:
            logger.info(f"  ✓ {var} is set")
    
    if missing_vars:
        logger.error(f"\nMissing environment variables: {', '.join(missing_vars)}")
        logger.error("Please set these variables before running setup.")
        return False
    
    logger.info("✓ All required environment variables are set\n")
    return True


def test_openbao_connection():
    """Test OpenBao connection and authentication"""
    logger.info("Testing OpenBao connection...")
    
    try:
        from rag_service import OpenBaoClient
        
        client = OpenBaoClient()
        client.authenticate()
        
        logger.info("✓ OpenBao authentication successful\n")
        return True
        
    except Exception as e:
        logger.error(f"✗ OpenBao connection failed: {e}\n")
        return False


def test_openbao_secrets():
    """Test fetching secrets from OpenBao"""
    logger.info("Testing OpenBao secrets retrieval...")
    
    try:
        from rag_service import OpenBaoClient
        
        client = OpenBaoClient()
        client.authenticate()
        
        # Test Pinecone secrets
        logger.info("  Checking Pinecone secrets...")
        pinecone_secrets = client.get_secrets_dict("secret/data/myapp/pinecone")
        if 'PINECONE_API_KEY' in pinecone_secrets:
            logger.info("    ✓ PINECONE_API_KEY found")
        else:
            logger.error("    ✗ PINECONE_API_KEY not found")
            return False
        
        if 'PINECONE_ENVIRONMENT' in pinecone_secrets:
            logger.info(f"    ✓ PINECONE_ENVIRONMENT: {pinecone_secrets['PINECONE_ENVIRONMENT']}")
        else:
            logger.warning("    ⚠ PINECONE_ENVIRONMENT not found (using default: us-east-1)")
        
        # Test Database secrets
        logger.info("  Checking Database secrets...")
        db_secrets = client.get_secrets_dict("secret/data/myapp/database")
        
        # Check for DATABASE_URL or ORDER_DB_PASSWORD (one is required)
        if 'DATABASE_URL' in db_secrets:
            logger.info(f"    ✓ DATABASE_URL found")
        elif 'ORDER_DB_PASSWORD' in db_secrets:
            logger.info(f"    ✓ ORDER_DB_PASSWORD found")
            # Check env vars for other params
            env_vars = ['ORDER_DB_HOST', 'ORDER_DB_NAME', 'ORDER_DB_USER']
            for var in env_vars:
                if os.getenv(var):
                    logger.info(f"    ✓ {var} found in environment")
                else:
                    logger.warning(f"    ⚠ {var} not in environment (using default)")
        else:
            logger.error(f"    ✗ Neither DATABASE_URL nor ORDER_DB_PASSWORD found")
            return False
        
        # Test OpenRouter secrets
        logger.info("  Checking OpenRouter secrets...")
        openrouter_secrets = client.get_secrets_dict("secret/data/myapp/openrouter")
        if 'OPENROUTER_API_KEY' in openrouter_secrets:
            logger.info("    ✓ OPENROUTER_API_KEY found")
        else:
            logger.warning("    ⚠ OPENROUTER_API_KEY not found (optional for RAG)")
        
        logger.info("✓ All required secrets are configured\n")
        return True
        
    except Exception as e:
        logger.error(f"✗ Failed to retrieve secrets: {e}\n")
        return False


def test_database_connection():
    """Test PostgreSQL database connection"""
    logger.info("Testing PostgreSQL database connection...")
    
    try:
        import psycopg2
        from rag_service import get_rag_service
        
        rag = get_rag_service()
        rag.bao_client.authenticate()
        conn_string = rag.get_database_connection_string()
        
        conn = psycopg2.connect(conn_string)
        cursor = conn.cursor()
        
        # Test merchant_items table
        cursor.execute("SELECT COUNT(*) FROM merchant_items")
        count = cursor.fetchone()[0]
        
        logger.info(f"  ✓ Connected to database")
        logger.info(f"  ✓ Found {count} items in merchant_items table")
        
        cursor.close()
        conn.close()
        
        if count == 0:
            logger.warning("  ⚠ No merchant items found - database may be empty\n")
        else:
            logger.info("✓ Database connection successful\n")
        
        return True
        
    except Exception as e:
        logger.error(f"✗ Database connection failed: {e}\n")
        return False


def test_embeddings():
    """Test HuggingFace embeddings model"""
    logger.info("Testing HuggingFace embeddings model...")
    logger.info("  (Note: First run will download ~500MB model)")
    
    try:
        from langchain_community.embeddings import HuggingFaceEmbeddings
        
        embeddings = HuggingFaceEmbeddings(
            model_name="nomic-ai/nomic-embed-text-v1.5",
            model_kwargs={'device': 'cpu', 'trust_remote_code': True},
            encode_kwargs={'normalize_embeddings': True}
        )
        
        # Test embedding
        test_text = "This is a test sentence for embeddings."
        embedding = embeddings.embed_query(test_text)
        
        logger.info(f"  ✓ Embedding model loaded successfully")
        logger.info(f"  ✓ Embedding dimension: {len(embedding)}")
        logger.info("✓ Embeddings test successful\n")
        return True
        
    except Exception as e:
        logger.error(f"✗ Embeddings test failed: {e}\n")
        return False


def initialize_rag_service():
    """Initialize RAG service and create Pinecone index"""
    logger.info("Initializing RAG service...")
    
    try:
        from rag_service import get_rag_service
        
        rag = get_rag_service()
        rag.initialize()
        
        logger.info("✓ RAG service initialized successfully\n")
        return True
        
    except Exception as e:
        logger.error(f"✗ RAG service initialization failed: {e}\n")
        return False


def run_test_ingestion():
    """Run a test ingestion of merchant items"""
    logger.info("Running test ingestion...")
    
    try:
        from ingest_data import ingest_merchant_items
        
        ingest_merchant_items(clear_existing=True)
        
        logger.info("✓ Test ingestion successful\n")
        return True
        
    except Exception as e:
        logger.error(f"✗ Test ingestion failed: {e}\n")
        return False


def run_test_search():
    """Run a test search query"""
    logger.info("Running test search...")
    
    try:
        from rag_service import get_rag_service
        
        rag = get_rag_service()
        rag.initialize()
        
        # Test search
        results = rag.similarity_search("food", k=3)
        
        logger.info(f"  ✓ Found {len(results)} results")
        
        if results:
            logger.info("  Sample result:")
            doc = results[0]
            logger.info(f"    Item: {doc.metadata['item_name']}")
            logger.info(f"    Merchant: {doc.metadata['merchant_name']}")
            logger.info(f"    Price: ${doc.metadata['price']:.2f}")
        
        logger.info("✓ Test search successful\n")
        return True
        
    except Exception as e:
        logger.error(f"✗ Test search failed: {e}\n")
        return False


def main():
    """Run all setup and tests"""
    logger.info("=" * 80)
    logger.info("RAG Module Setup and Test")
    logger.info("=" * 80 + "\n")
    
    tests = [
        ("Environment Variables", check_environment),
        ("OpenBao Connection", test_openbao_connection),
        ("OpenBao Secrets", test_openbao_secrets),
        ("Database Connection", test_database_connection),
        ("Embeddings Model", test_embeddings),
        ("RAG Service Initialization", initialize_rag_service),
        ("Test Ingestion", run_test_ingestion),
        ("Test Search", run_test_search),
    ]
    
    results = {}
    
    for test_name, test_func in tests:
        try:
            results[test_name] = test_func()
        except Exception as e:
            logger.error(f"Unexpected error in {test_name}: {e}")
            results[test_name] = False
        
        # Stop on critical failures
        if not results[test_name] and test_name in ["Environment Variables", "OpenBao Connection", "OpenBao Secrets"]:
            logger.error(f"\nCritical test failed: {test_name}")
            logger.error("Please fix the above issues before continuing.")
            break
    
    # Summary
    logger.info("\n" + "=" * 80)
    logger.info("Test Summary")
    logger.info("=" * 80)
    
    passed = sum(1 for v in results.values() if v)
    total = len(results)
    
    for test_name, result in results.items():
        status = "✓ PASS" if result else "✗ FAIL"
        logger.info(f"{status:8} {test_name}")
    
    logger.info(f"\nPassed: {passed}/{total}")
    
    if passed == total:
        logger.info("\n🎉 All tests passed! RAG system is ready to use.")
        logger.info("\nNext steps:")
        logger.info("  1. Start the MCP server: python mcp-server/main.py")
        logger.info("  2. Use search_merchant_items() tool from MCP client")
        logger.info("  3. Run manual searches: cd rag && python ingest_data.py search 'your query'")
        return 0
    else:
        logger.error("\n❌ Some tests failed. Please review the errors above.")
        return 1


if __name__ == "__main__":
    sys.exit(main())
