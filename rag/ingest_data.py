"""
Data ingestion script for populating Pinecone index with merchant items
Fetches merchant_items from PostgreSQL and indexes them for RAG search
"""

import psycopg2
import logging
from typing import List, Dict
from langchain_core.documents import Document
from rag_service import get_rag_service
from dotenv import load_dotenv
load_dotenv()

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


def fetch_merchant_items(conn_string: str) -> List[Dict]:
    """
    Fetch all merchant items from PostgreSQL
    
    Args:
        conn_string: PostgreSQL connection string
        
    Returns:
        List of merchant item dictionaries
    """
    logger.info("Connecting to PostgreSQL database...")
    
    try:
        conn = psycopg2.connect(conn_string)
        cursor = conn.cursor()
        
        # Fetch merchant items with merchant information
        query = """
            SELECT 
                mi.item_id,
                mi.merchant_id,
                mi.name,
                mi.description,
                mi.price,
                mi.quantity,
                mi.created_at,
                mi.updated_at,
                m.name as merchant_name
            FROM merchant_items mi
            JOIN merchants m ON mi.merchant_id = m.merchant_id
            WHERE mi.quantity > 0
            ORDER BY mi.merchant_id, mi.item_id
        """
        
        cursor.execute(query)
        rows = cursor.fetchall()
        
        items = []
        for row in rows:
            item = {
                'item_id': row[0],
                'merchant_id': row[1],
                'item_name': row[2],
                'description': row[3],
                'price': float(row[4]) if row[4] else 0.0,
                'quantity': row[5],
                'created_at': row[6].isoformat() if row[6] else None,
                'updated_at': row[7].isoformat() if row[7] else None,
                'merchant_name': row[8]
            }
            items.append(item)
        
        cursor.close()
        conn.close()
        
        logger.info(f"✓ Fetched {len(items)} merchant items from database")
        return items
        
    except Exception as e:
        logger.error(f"Failed to fetch merchant items: {e}")
        raise


def create_documents_from_items(items: List[Dict]) -> List[Document]:
    """
    Convert merchant items to Langchain Documents for indexing
    
    Args:
        items: List of merchant item dictionaries
        
    Returns:
        List of Langchain Document objects
    """
    documents = []
    
    for item in items:
        # Create rich text content for embedding
        text_content = f"""
Item: {item['item_name']}
Description: {item['description'] or 'No description available'}
Price: ${item['price']:.2f}
Stock: {item['quantity']} units available
Merchant: {item['merchant_name']}
        """.strip()
        
        # Metadata for filtering and retrieval
        metadata = {
            'item_id': item['item_id'],
            'merchant_id': item['merchant_id'],
            'merchant_name': item['merchant_name'],
            'item_name': item['item_name'],
            'price': item['price'],
            'quantity': item['quantity']
        }
        
        doc = Document(
            page_content=text_content,
            metadata=metadata
        )
        documents.append(doc)
    
    logger.info(f"✓ Created {len(documents)} documents for indexing")
    return documents


def ingest_merchant_items(clear_existing: bool = False):
    """
    Main ingestion function: fetch items from DB and index them in Pinecone
    
    Args:
        clear_existing: If True, clear existing index before ingestion
    """
    logger.info("Starting merchant items ingestion...")
    
    try:
        # Initialize RAG service
        rag_service = get_rag_service()
        rag_service.initialize()
        
        # Clear existing index if requested
        if clear_existing:
            logger.info("Clearing existing index...")
            rag_service.clear_index()
        
        # Get database connection string from OpenBao
        conn_string = rag_service.get_database_connection_string()
        
        # Fetch merchant items
        items = fetch_merchant_items(conn_string)
        
        if not items:
            logger.warning("No merchant items found in database")
            return
        
        # Convert to documents
        documents = create_documents_from_items(items)
        
        # Index documents in Pinecone
        logger.info("Indexing documents in Pinecone...")
        rag_service.add_documents(documents)
        
        logger.info(f"✓ Successfully ingested {len(documents)} merchant items into Pinecone")
        logger.info("✓ RAG system is ready for queries")
        
    except Exception as e:
        logger.error(f"Ingestion failed: {e}")
        raise


def search_merchant_items(query: str, k: int = 5, merchant_id: int = None):
    """
    Search merchant items using RAG
    
    Args:
        query: Search query
        k: Number of results to return
        merchant_id: Optional merchant ID filter
    """
    try:
        rag_service = get_rag_service()
        rag_service.initialize()
        
        # Build filter if merchant_id provided
        filter_dict = None
        if merchant_id:
            filter_dict = {"merchant_id": merchant_id}
        
        # Perform search with scores
        results = rag_service.similarity_search_with_score(
            query=query,
            k=k,
            filter=filter_dict
        )
        
        logger.info(f"\nSearch results for: '{query}'")
        logger.info("=" * 80)
        
        for i, (doc, score) in enumerate(results, 1):
            logger.info(f"\n{i}. Score: {score:.4f}")
            logger.info(f"   Item: {doc.metadata.get('item_name', 'Unknown')}")
            logger.info(f"   Merchant: {doc.metadata.get('merchant_name', 'Unknown')}")
            logger.info(f"   Price: ${doc.metadata.get('price', 0):.2f}")
            if 'category' in doc.metadata and doc.metadata['category']:
                logger.info(f"   Category: {doc.metadata['category']}")
            logger.info(f"   Stock: {doc.metadata.get('stock_quantity', 0)}")
            logger.info(f"\n   {doc.page_content[:200]}...")
        
        return results
        
    except Exception as e:
        logger.error(f"Search failed: {e}")
        raise


if __name__ == "__main__":
    import sys
    
    if len(sys.argv) > 1:
        command = sys.argv[1]
        
        if command == "ingest":
            # Ingest merchant items
            clear = "--clear" in sys.argv
            ingest_merchant_items(clear_existing=clear)
            
        elif command == "search":
            # Search example
            if len(sys.argv) < 3:
                print("Usage: python ingest_data.py search <query> [--merchant-id=<id>]")
                sys.exit(1)
            
            query = sys.argv[2]
            merchant_id = None
            
            for arg in sys.argv[3:]:
                if arg.startswith("--merchant-id="):
                    merchant_id = int(arg.split("=")[1])
            
            search_merchant_items(query, k=5, merchant_id=merchant_id)
        
        else:
            print(f"Unknown command: {command}")
            print("Available commands:")
            print("  ingest [--clear]  - Ingest merchant items into Pinecone")
            print("  search <query>    - Search merchant items")
            sys.exit(1)
    else:
        print("Usage:")
        print("  python ingest_data.py ingest [--clear]")
        print("  python ingest_data.py search <query> [--merchant-id=<id>]")
        sys.exit(1)
