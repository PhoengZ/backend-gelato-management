# GelatoFlow — Project Description and ADRs

**Gelato Preorder, Pickup, and Batch Inventory Management System**  
*ระบบสั่งเจลาโตล่วงหน้า รับสินค้าที่ร้าน และบริหารสินค้าคงเหลือแบบราย Batch*

---

## สารบัญ (Table of Contents)

1. [Project Name](#1-project-name)
2. [Group Members](#2-group-members)
3. [Problem Description](#3-problem-description)
   - 3.1 [Project Objectives](#31-project-objectives)
   - 3.2 [Project Scope](#32-project-scope)
4. [Target Customers & Actors](#4-target-customers--actors)
   - 4.1 [Primary Customers](#41-primary-customers)
   - 4.2 [Operational Users](#42-operational-users)
   - 4.3 [External Systems](#43-external-systems)
5. [Scenarios and Business Use Cases](#5-scenarios-and-business-use-cases)
   - [UC-01 — Place Gelato Preorder with Time Slot](#uc-01--place-gelato-preorder-with-time-slot)
   - [UC-02 — Prepare and Complete Store Pickup via Time-Slot Queue](#uc-02--prepare-and-complete-store-pickup-via-time-slot-queue)
   - [UC-03 — Manage Batch Inventory](#uc-03--manage-batch-inventory)
6. [Use Case Diagram](#6-use-case-diagram)
7. [Functional Requirements (FR-01 ถึง FR-17)](#7-functional-requirements)
8. [Non-functional Requirements (NFR-01 ถึง NFR-13)](#8-non-functional-requirements)
9. [Architecture Decision Records (ADRs)](#9-architecture-decision-records-adrs)
   - [ADR-001 — Decompose the System by Business Capability](#adr-001--decompose-the-system-by-business-capability)
   - [ADR-002 — Make Batch Inventory the Single Source of Truth for Stock](#adr-002--make-batch-inventory-the-single-source-of-truth-for-stock)
   - [ADR-003 — Use Synchronous gRPC for Order-to-Inventory Reservation](#adr-003--use-synchronous-grpc-for-order-to-inventory-reservation)
   - [ADR-004 — Use RabbitMQ for Non-blocking Domain Events](#adr-004--use-rabbitmq-for-non-blocking-domain-events)
   - [ADR-005 — Expose Client APIs through REST and an API Gateway](#adr-005--expose-client-apis-through-rest-and-an-api-gateway)
   - [ADR-006 — Expose Catalog through REST and gRPC using Shared Application Logic](#adr-006--expose-catalog-through-rest-and-grpc-using-shared-application-logic)
   - [ADR-007 — Implement Inventory Transactions and Explicit Caller Boundaries](#adr-007--implement-inventory-transactions-and-explicit-caller-boundaries)

---

## 1. Project Name

**GelatoFlow — Gelato Preorder, Pickup, and Batch Inventory Management System**  
ระบบสั่งเจลาโตล่วงหน้า รับสินค้าที่ร้าน และบริหารสินค้าคงเหลือแบบราย Batch

---

## 2. Group Members

| No. | Student ID / รหัสนิสิต | Name–Surname / ชื่อ–นามสกุล |
|:---:|:---------------------:|:---------------------------|
| 1 | 6631337421 | Pornmongkol Tonnikorn |
| 2 | 6630016821 | Kritsada Chan-Inn |
| 3 | 6632088321 | Tinnapat Sittisuwan |
| 4 | 6632154021 | Peeradon Thienpongkasem |
| 5 | 6630199021 | Phaolap Kulteera |

---

## 3. Problem Description

ร้านเจลาโตมีข้อจำกัดด้านสินค้าคงเหลือที่แตกต่างจากสินค้าทั่วไป เพราะสินค้าแต่ละรสผลิตเป็น Batch มีจำนวน portion จำกัด มีวันหมดอายุ และอาจเกิด waste ระหว่างวัน ในช่วงที่มีลูกค้าจำนวนมาก ลูกค้าไม่ทราบล่วงหน้าว่ารสชาติที่ต้องการยังมีอยู่หรือไม่ ต้องรอคิวที่หน้าร้าน และอาจเดินทางมาแล้วพบว่าสินค้าหมด ขณะที่พนักงานต้องประสานคำสั่งซื้อ การชำระเงิน คิวเตรียมสินค้า และจำนวนคงเหลือด้วยหลายขั้นตอน

ปัญหาที่มีผลกระทบสูงที่สุดคือ **Overselling** หากลูกค้าหลายคนสั่ง portion สุดท้ายพร้อมกัน ระบบที่ตรวจสอบและตัด stock แยกขั้นตอนกันอาจยืนยันหลาย order เกินจำนวนจริง ทำให้เกิด negative stock การยกเลิกคำสั่งซื้อ หรือประสบการณ์ที่ไม่ดีต่อลูกค้า

นอกจากนี้ ในระบบการสั่งและรับสินค้าหน้าร้านแบบดั้งเดิม การที่ลูกค้าเดินทางมาถึงโดยไม่มีการนัดหมายช่วงเวลาล่วงหน้า มักก่อให้เกิดปัญหาคิวแออัดหน้าร้านแบบกะทันหัน (**Arrival Spikes**) พนักงานในครัวจัดลำดับการเตรียมสินค้าได้ยาก และเจลาโตเป็นของหวานที่ละลายเร็วและไวต่ออุณหภูมิ จึงไม่สามารถตักเตรียมทิ้งไว้ล่วงหน้านานเกินไปได้

GelatoFlow แก้ปัญหานี้ด้วยสถาปัตยกรรมแบบ **Event-Driven Microservices** และระบบบริหารจัดการคิวตามช่วงเวลารับสินค้า (**Time-Slot Queue Management System**) โดยลูกค้าสามารถตรวจสอบความพร้อมของสินค้าและเลือกช่วงเวลารับสินค้าที่ต้องการ (เช่น 14:00 - 14:15 น.) เมื่อชำระเงินสำเร็จ ระบบจะจัดสรรหมายเลขคิวเฉพาะเจาะจงภายใน Time Slot นั้น ๆ (เช่น Slot 14:00 - 14:15 -> Queue #01, #02) ทำให้การกระจายตัวของคิวเป็นระเบียบ หน้าร้านไม่แออัด และพนักงานสามารถตักเจลาโตตามลำดับคิวในแต่ละรอบเวลาได้อย่างสดใหม่ โดยมี **Batch Inventory Service** เป็นผู้ตัดสินใจเรื่อง stock เพียงจุดเดียว

### Primary Business Flow / กระบวนการหลัก

```text
Browse Gelato & Time Slots 
  -> Check Availability 
  -> Select Slot & Place Order 
  -> Reserve Stock (Atomic FEFO) 
  -> Pay (Stripe Checkout Session / PromptPay) 
  -> Slot Queue Allocation 
  -> Prepare by Slot 
  -> Ready Notification (SSE) 
  -> Complete Pickup in Slot
```

### 3.1 Project Objectives
- ลดเวลารอและความแออัดหน้าร้านด้วยระบบเลือกช่วงเวลารับ (Time Slot) และจัดคิวประจำ Slot
- ป้องกัน overselling และ negative stock ภายใต้ concurrent requests ด้วย Batch Inventory Service
- ช่วยให้พนักงานเห็น preparation queue จัดกลุ่มตาม Time Slot และลำดับคิวอย่างเป็นระบบ
- บริหารการผลิต การจอง การขาย waste และวันหมดอายุในระดับ batch
- บันทึกข้อมูลที่ตรวจสอบย้อนหลังได้เพื่อวางแผนผลิตและลด waste

### 3.2 Project Scope

| In scope / อยู่ในขอบเขต | Out of scope / นอกขอบเขต |
|---|---|
| 1–2 สาขา, 8–12 flavors | Delivery and route planning |
| Web preorder and store pickup | IoT freezer/sensor integration |
| Pickup time-slot selection and slot-scoped queue management | Complex loyalty tiers |
| Stripe Checkout Session integration (PromptPay / Card in THB) | Multi-country tax and settlement |
| Batch inventory, atomic reservation (FEFO), waste, expiration | Enterprise-scale analytics platform |

---

## 4. Target Customers & Actors

### 4.1 Primary Customers
- **Customer / ลูกค้าร้านเจลาโต:** ผู้ที่ต้องการสั่งและเลือกรอบเวลารับสินค้าล่วงหน้า ลดเวลารอหน้าร้าน และมั่นใจว่าสินค้าถูกจองไว้แล้ว ใช้งานผ่านเว็บเพื่อตรวจสอบสต็อก เลือกรอบเวลารับ (Pickup Time Slot) ชำระเงินผ่านระบบออนไลน์ (Stripe PromptPay/Card) และรับหมายเลขคิวประจำ Slot

### 4.2 Operational Users
- **Store Staff / พนักงานร้าน:** รับคำสั่งซื้อจาก preparation queue ที่จัดกลุ่มตาม Time Slot และหมายเลขคิว ตักเตรียมเจลาโตตามรอบเวลา และยืนยันการส่งมอบด้วยหมายเลขคิวประจำ Slot
- **Store Manager / ผู้จัดการร้าน:** จัดการ flavor, recipe, allergen และ pickup time slots; บันทึก batch ใหม่พร้อม `batch_code`; บันทึก waste; ตรวจ low stock และ expiration

### 4.3 External Systems
- **Stripe Payment Gateway:** ผู้ให้บริการ Payment Gateway ภายนอก รับคำขอสร้าง Checkout Session และส่งผลการชำระเงินผ่าน Webhook ยืนยันผลแบบ Asynchronous

---

## 5. Scenarios and Business Use Cases

### UC-01 — Place Gelato Preorder with Time Slot

| Item | Description |
|---|---|
| **Primary actor** | Customer / ลูกค้า |
| **Goal** | เลือกเจลาโตที่มีจำหน่าย จอง portion เลือกรอบเวลารับสินค้า (Pickup Time Slot) ชำระเงิน และได้รับ order confirmation พร้อมหมายเลขคิวประจำ Slot |
| **Trigger** | ลูกค้าเลือกสินค้า เลือกรอบเวลารับสินค้า และกดยืนยันการสั่งซื้อ |
| **Preconditions** | ร้านและ pickup time slot เปิดรับคำสั่งซื้อ; Catalog, Batch Inventory, Order และ Payment Service พร้อมใช้งาน |
| **Success result** | ระบบยืนยัน order, reservation, การชำระเงินสำเร็จ และจัดสรรหมายเลขคิวประจำ Slot โดยไม่มีการขายเกิน stock |

#### Main scenario / ขั้นตอนหลัก:
1. ลูกค้าตรวจสอบรายการเจลาโต ความพร้อมของสต็อกคงเหลือ และรอบเวลารับสินค้าที่เปิดรับ
2. ลูกค้าเลือกรสชาติ จำนวน และระบุรอบเวลารับสินค้าที่ต้องการ (เช่น 14:00 - 14:15 น.) พร้อมส่งคำสั่งซื้อผ่าน API Gateway
3. Order Service ตรวจสอบความถูกต้องและราคาจาก Catalog Service จากนั้นเรียก Batch Inventory Service เพื่อจองสต็อกสินค้าทันที (`ReservePortions`) ตามนโยบาย FEFO
4. Order Service บันทึกสถานะ `PENDING_PAYMENT` พร้อม reservation ID และเรียก Payment Service ผ่าน gRPC (`CreateCheckoutSession`) เพื่อสร้าง Stripe Checkout Session
5. ลูกค้าได้รับ `checkout_url` และทำการชำระเงินผ่าน Stripe Checkout (PromptPay หรือบัตรเครดิต)
6. เมื่อลูกค้าชำระเงินสำเร็จ Stripe ส่ง signed webhook มายัง Payment Service ซึ่งตรวจสอบลายเซ็นแล้วเรียก HTTP callback กลับมายัง Order Service (`payment-succeeded`)
7. Order Service ปรับสถานะเป็น `PAYMENT_CONFIRMING` และเรียก Batch Inventory Service ยืนยันการตัดสต็อกถาวร (`ConfirmReservation`)
8. เมื่อตัดสต็อกสำเร็จ Order Service บันทึกสถานะเป็น `PAID` พร้อมบันทึก domain event `order.placed` ลงใน Transactional Outbox
9. ระบบจัดสรรหมายเลขคิวต่อเนื่องเฉพาะช่วงเวลานั้น (เช่น Slot 14:00 - 14:15 น. ได้รับคิว #01) และส่งข้อมูลเข้าคิวเตรียมสินค้าของ Fulfillment Service
10. Notification Service ส่งการแจ้งเตือนยืนยันคำสั่งซื้อ รายละเอียดรอบเวลารับ และหมายเลขคิวประจำ Slot ให้ลูกค้าทราบผ่าน Server-Sent Events (SSE)

#### Alternative/exception flows / กรณีทางเลือก:
- หากสต็อกไม่เพียงพอ Batch Inventory จะปฏิเสธการจองทันที ลูกค้าจะไม่สามารถสั่งซื้อได้
- หากการชำระเงินไม่สำเร็จ หมดเวลา หรือลูกค้ายกเลิก Order Service จะปรับสถานะเป็น `CANCEL_PENDING`, เรียกปล่อยสต็อกคืน (`ReleaseReservation`), และปรับเป็น `CANCELLED`
- หากคำสั่งซื้อเดิมถูกส่งซ้ำด้วย `Idempotency-Key` เดิม ระบบจะส่งผลลัพธ์เดิมกลับไปโดยไม่สร้างคำสั่งซื้อหรือจองสต็อกซ้ำ
- หากรอบเวลารับสินค้า (Time Slot) เต็ม ระบบจะแจ้งเตือนให้ลูกค้าเลือกรอบเวลาใหม่

---

### UC-02 — Prepare and Complete Store Pickup via Time-Slot Queue

| Item | Description |
|---|---|
| **Primary actor** | Store Staff / พนักงานร้าน |
| **Supporting actor** | Customer / ลูกค้า |
| **Goal** | พนักงานในครัวเตรียม order ตามรอบเวลาและหมายเลขคิว แจ้งสถานะพร้อมรับ และส่งมอบสินค้าตามหมายเลขคิวเมื่อลูกค้ามาถึงในรอบเวลา |
| **Trigger** | Staff เปิด preparation queue dashboard หรือถึงรอบเวลาของ pickup time slot |
| **Preconditions** | Order ชำระเงินแล้ว (PAID) และระบบจัดสรรหมายเลขคิวประจำ Slot เรียบร้อยใน Fulfillment Service |
| **Success result** | ลูกค้าได้รับสินค้าตามรอบเวลา สถานะเปลี่ยนเป็น `COMPLETED` และวงจรการส่งมอบเสร็จสมบูรณ์ |

#### Main scenario / ขั้นตอนหลัก:
1. พนักงานร้านเปิดดูหน้าจอรายการเตรียมสินค้าที่จัดกลุ่มและเรียงตามรอบเวลา (Time Slot) และหมายเลขคิว
2. พนักงานเริ่มตักเตรียมเจลาโตตามลำดับคิวในรอบเวลานั้น และปรับสถานะเป็น `PREPARING`
3. เมื่อเตรียมเสร็จ พนักงานเปลี่ยนสถานะเป็น `READY_FOR_PICKUP` ซึ่งส่ง event `fulfillment.order_ready`
4. Notification Service ส่งสัญญาณ popup แจ้งเตือนลูกค้าผ่านเบราว์เซอร์ทันทีว่าสินค้าพร้อมรับแล้ว
5. ลูกค้าเดินทางมาถึงร้านในช่วงเวลาที่นัดหมาย และแสดงหมายเลขคิวประจำ Slot หรือ Order ID
6. พนักงานตรวจสอบความถูกต้อง ส่งมอบสินค้าให้ลูกค้า และบันทึกสถานะเป็น `COMPLETED` (`PICKED_UP`)

---

### UC-03 — Manage Batch Inventory

| Item | Description |
|---|---|
| **Primary actor** | Store Manager / ผู้จัดการร้าน |
| **Goal** | บันทึกการผลิต ติดตามคงเหลือ บันทึก waste และหยุดจำหน่าย batch หมดอายุ |
| **Trigger** | มีการผลิต batch ใหม่, พบ waste หรือต้องตรวจสอบ stock |
| **Preconditions** | ผู้จัดการมีสิทธิ์ผ่านการยืนยันตัวตนด้วยบทบาท `MANAGER` |
| **Success result** | ยอด stock และประวัติ batch ถูกต้อง ตรวจสอบย้อนหลังได้ และ batch หมดอายุไม่ถูกนำไปขาย |

#### Main scenario / ขั้นตอนหลัก:
1. ผู้จัดการร้านสร้าง Batch การผลิตใหม่ ระบุรสชาติ จำนวนผลิต วันผลิต วันหมดอายุ และ `batch_code`
2. ระบบตรวจสอบความถูกต้องว่ารสชาตินั้นมีอยู่จริงและ Active ใน Catalog Service จากนั้นบันทึกข้อมูล Batch
3. ผู้จัดการร้านตรวจสอบสถานะสต็อกแบบเรียลไทม์ ทั้งยอดผลิต ยอดจอง ยอดขาย ของเสีย และจำนวนคงเหลือพร้อมขาย
4. เมื่อเกิดของเสีย ผู้จัดการร้านระบุ Batch จำนวน และสาเหตุเพื่อบันทึกของเสีย (`recordWaste`)
5. ระบบคำนวณยอดคงเหลือใหม่ตาม Invariant และส่ง event `inventory.waste` เข้า Transactional Outbox
6. Batch ที่หมดอายุจะถูกตัดออกจากการจำหน่ายโดยอัตโนมัติผ่าน Periodic Expiry Worker

---

## 6. Use Case Diagram

```mermaid
flowchart LR
    Customer((Customer))
    Staff((Store Staff))
    Manager((Store Manager))
    StripeSystem["Stripe Gateway"]

    subgraph GelatoFlow["GelatoFlow System"]
        UC01["UC-01: Place Gelato Preorder with Time Slot"]
        UC02["UC-02: Prepare & Complete Pickup via Time-Slot Queue"]
        UC03["UC-03: Manage Batch Inventory"]
        
        SubBrowse["Browse Menu & Time Slots"]
        SubAvail["Check Availability"]
        SubReserve["Reserve Stock (FEFO)"]
        SubPay["Process Payment (Stripe Checkout)"]
        SubTicket["Mark Order Ready & Complete"]
        SubWaste["Record Waste & Track Expiry"]
    end

    Customer --> UC01
    Customer --> SubBrowse
    Customer --> SubAvail

    UC01 -.->|include| SubReserve
    UC01 -.->|include| SubPay

    Staff --> UC02
    UC02 -.->|include| SubTicket

    Manager --> UC03
    UC03 -.->|include| SubWaste

    SubPay <--> StripeSystem
```

---

## 7. Functional Requirements

| ID | Functional requirement | Related use case |
|:---:|---|:---:|
| **FR-01** | ระบบต้องแสดง flavor, price, availability (สต็อกคงเหลือ) และ pickup time slots ที่เปิดรับตามสาขา | UC-01 |
| **FR-02** | ลูกค้าต้องเลือก quantity, customization และระบุช่วงเวลารับสินค้า (Pickup Time Slot) ได้ | UC-01 |
| **FR-03** | ระบบต้องตรวจและ reserve stock ก่อนยืนยัน order ด้วยนโยบาย FEFO | UC-01 |
| **FR-04** | ระบบต้องรองรับการชำระเงินผ่าน Stripe Checkout Session (PromptPay / Credit Card) | UC-01 |
| **FR-05** | คำขอสร้าง order ซ้ำด้วย `Idempotency-Key` เดิมต้องไม่สร้าง order หรือ reservation ซ้ำ | UC-01 |
| **FR-06** | เมื่อ order ชำระเงินสำเร็จ ระบบต้องจัดสรรหมายเลขคิวต่อเนื่องเฉพาะภายใน Time Slot นั้น (Slot Queue Number) | UC-01 |
| **FR-07** | ลูกค้าต้องดู order status, รายละเอียดรอบเวลา (Time Slot) และหมายเลขคิวประจำ Slot ได้ | UC-01, UC-02 |
| **FR-08** | Staff ต้องดู preparation queue ที่จัดกลุ่มและจัดลำดับตาม Pickup Time Slot และ Slot Queue Number ได้ | UC-02 |
| **FR-09** | Staff ต้องเปลี่ยนสถานะตาม transition ที่อนุญาต (`PREPARING` -> `READY_FOR_PICKUP` -> `COMPLETED`) | UC-02 |
| **FR-10** | ระบบต้องแจ้งเตือนลูกค้าเมื่อ order ยืนยันพร้อมคิวประจำ Slot และเมื่อ order เปลี่ยนเป็น `READY_FOR_PICKUP` ผ่าน SSE | UC-01, UC-02 |
| **FR-11** | Staff ต้องตรวจหมายเลขคิวประจำ Slot และยืนยันการรับสินค้า (Complete Pickup) ได้ | UC-02 |
| **FR-12** | Manager ต้องสร้าง batch พร้อมจำนวนผลิต วันหมดอายุ และ production `batch_code` ได้ | UC-03 |
| **FR-13** | ระบบต้องแสดง produced, reserved, sold, waste และ available ต่อ batch แบบเรียลไทม์ | UC-03 |
| **FR-14** | Manager ต้องบันทึก waste พร้อม quantity และ reason ได้ | UC-03 |
| **FR-15** | ระบบต้องไม่นำ batch ที่หมดอายุไป allocate หรือ reserve ให้ order ใหม่ | UC-01, UC-03 |
| **FR-16** | ระบบต้องสร้าง low-stock และ batch-expiration alert เมื่อสต็อกใกล้หมดหรือใกล้หมดอายุ | UC-03 |
| **FR-17** | ระบบต้องเก็บ audit record สำหรับการเปลี่ยนแปลงข้อมูลสำคัญและการปรับปรุงสต็อก | UC-03 |

---

## 8. Non-functional Requirements

| ID | Quality Attribute | Requirement and Measurable Criterion |
|:---:|:---:|---|
| **NFR-01** | **Consistency** | ทุก batch ต้องรักษา Invariant: $Available = Produced - Reserved - Sold - Waste$ และ $Available \ge 0$ เสมอ ไม่มียอดติดลบ |
| **NFR-02** | **Concurrency Reliability** | เมื่อมีสินค้าคงเหลือ $Available = 1$ portion และมี 20 concurrent order requests แย่งจองพร้อมกัน ระบบต้องตัดจองสำเร็จเพียง 1 รายการ และ Available ต้องคงเหลือ 0 ไม่ติดลบ |
| **NFR-03** | **Performance** | Order Creation API ต้องมีค่า Latency **$p95 \le 1.0$ วินาที** ภายใต้โหลดทดสอบ MVP ที่กำหนด |
| **NFR-04** | **Availability & Fault Isolation** | ความล้มเหลวของ downstream services (เช่น Notification Service ล่ม) ต้องไม่ทำให้คำสั่งซื้อที่จ่ายเงินแล้ว (Confirmed Order) สูญหายหรือล้มเหลว |
| **NFR-05** | **Reliability & Messaging** | การส่งข้อมูลแบบ Asynchronous ผ่าน RabbitMQ ต้องใช้ Transactional Outbox; Consumer ทุกตัวต้องรองรับ At-least-once delivery โดยทำ Idempotent Deduplication ผ่าน CloudEvents `id` และต้องมีระบบ Dead Letter Queue (DLQ) สำหรับ Poison messages |
| **NFR-06** | **Security & Access Control** | การเชื่อมต่อต้องเข้ารหัสด้วย TLS; ใช้ Role-Based Access Control (`CUSTOMER`, `STAFF`, `MANAGER`); รหัสผ่านต้องเข้ารหัสด้วย Adaptive Hash (bcrypt); ห้ามเก็บ Plaintext Password หรือข้อมูลบัตรเครดิตในระบบเด็ดขาด |
| **NFR-07** | **Privacy & Log Redaction** | Application Log ต้อง Redact ข้อมูลสำคัญโดยอัตโนมัติ เช่น Bearer Access Tokens, Machine Secrets, Stripe Webhook Signatures และข้อมูลส่วนบุคคล (PII) ที่ไม่จำเป็น |
| **NFR-08** | **Auditability** | ทุกการปรับปรุง Batch, การบันทึก Waste, การเปลี่ยนแปลงข้อมูลสารก่อภูมิแพ้ (Allergen), และ Privileged Actions ของ Staff/Manager ต้องมี Audit Record ตรวจสอบย้อนหลังได้ |
| **NFR-09** | **Modifiability & Boundary** | Service อื่นต้องเข้าถึงข้อมูลผ่าน API/gRPC Contract ของ Service เจ้าของข้อมูลเท่านั้น ห้ามอ่าน/เขียน Private Database ของ Service อื่นโดยตรง และห้ามทำ Duplicate/Shadow Tables ข้าม Service |
| **NFR-10** | **Monetary Precision** | ข้อมูลจำนวนเงินทั้งหมดต้องเก็บและคำนวณในรูป **Integer Minor Units** (เช่น Satang ใน THB) ร่วมกับระบุ ISO 4217 Currency อย่างชัดเจน ห้ามใช้ Floating-point decimals ในการเก็บหรือส่งต่อข้อมูลเงินเด็ดขาด |
| **NFR-11** | **Idempotency Standard** | ทุก Mutation REST Endpoint ต้องรองรับ Header `Idempotency-Key` (UUID); การส่งคำขอเดิมซ้ำต้อง Replay ผลลัพธ์เดิมที่สำเร็จ; หากส่งคำขอที่มี payload ขัดแย้งกันด้วย key เดิมต้องตอบกลับ HTTP 409 Conflict |
| **NFR-12** | **Stateless Architecture** | Notification Service (SSE) และ Payment Service ต้องออกแบบเป็น Stateless Service โดยไม่ถือครอง Local Database ของตนเอง เพื่อรองรับการ Restart และ Horizontal Scaling ได้อย่างอิสระ |
| **NFR-13** | **Network & Container Isolation** | พอร์ตของฐานข้อมูลระดับ Operational (PostgreSQL, Redis, MongoDB) และ RabbitMQ Broker ต้องถูกจำกัดสิทธิ์ให้อยู่ภายใน Docker Internal Network เท่านั้นในโหมด Production ห้ามเปิด Public Port สู่ Internet ภายนอก |

---

## 9. Architecture Decision Records (ADRs)

### ADR-001 — Decompose the System by Business Capability
- **Status:** Accepted (Revised with Dedicated Auth Service)
- **Context:** GelatoFlow ประกอบด้วยขอบเขตงานทางธุรกิจที่แตกต่างกัน ได้แก่ Identity/Authentication, Catalog, Ordering, Batch Inventory, Payment, Fulfillment, Notification, และ Analytics การรวมระบบไว้ใน Monolith เดียวจะทำให้เกิด Concurrency Bottleneck, ขาดความเป็นเจ้าของข้อมูล (Data Ownership), และขยายระบบตามโหลดงานเฉพาะส่วนไม่ได้
- **Decision:** Decompose ระบบออกเป็น 8 บริการย่อยอิสระตาม Business Capability:
  1. **API Gateway:** Routing กลาง, ล้าง Spoofed Identity Headers, ตรวจสอบ JWT ด่านแรก, จัดการ CORS
  2. **Auth Service:** บริหารจัดการบัญชีผู้ใช้, จัดการสิทธิ์ RBAC, และออก Signed JWT Access Token (HS256)
  3. **Catalog Service:** ดูแลข้อมูลรสชาติเจลาโต, ราคา, สูตร, สารก่อภูมิแพ้ (เก็บใน Redis)
  4. **Order Service:** ดูแลวงจรคำสั่งซื้อ, Snapshot ราคา, เชื่อมโยง Session การจ่ายเงิน, จัดการ Outbox
  5. **Batch Inventory Service:** เป็น Single Source of Truth เพียงแห่งเดียวสำหรับล็อตการผลิต สต็อกคงเหลือ การจอง (FEFO) และของเสีย
  6. **Payment Service:** Stateless Adapter เชื่อมต่อกับ Stripe API, จัดการ Checkout Session gRPC และตรวจสอบ Webhook
  7. **Fulfillment Service:** ดูแลคิวเตรียมสินค้าในครัว จัดการคิวตาม Time Slot และการส่งมอบสินค้า
  8. **Notification Service:** Stateless Service แจ้งเตือนลูกค้าผ่าน Server-Sent Events (SSE)
  9. **Analytics Service:** รับ Asynchronous Events เพื่อคำนวณสถิติยอดขายและของเสียใน MongoDB
- **Consequences:** ความรับผิดชอบและขอบเขตข้อมูลชัดเจน แยก Deploy และขยายระบบตามส่วนงานได้ แต่ต้องการการสื่อสารข้าม Service ที่รัดกุม

---

### ADR-002 — Make Batch Inventory the Single Source of Truth for Stock
- **Status:** Accepted
- **Context:** ความเสี่ยงหลักของระบบคือ Overselling เมื่อหลายคำสั่งซื้อพยายามซื้อ portion สุดท้ายพร้อมกัน หาก Order Service หรือ Service อื่นสามารถแก้ไขหรือคำนวณสต็อกได้เอง จะไม่สามารถการันตี Invariant ได้
- **Decision:** ให้ Batch Inventory Service เป็นผู้ตัดสินใจเรื่องสต็อกแต่เพียงผู้เดียว ข้อมูลสต็อกทั้งหมดเป็น Private Database การจองใช้ Atomic PostgreSQL Transaction ร่วมกับ Advisory Lock แยกตาม Flavor และห้าม Service อื่นเข้าถึงฐานข้อมูลของ Inventory โดยตรง
- **Consequences:** บังคับ Invariant $Available = Produced - Reserved - Sold - Waste \ge 0$ ได้อย่างแม่นยำ ป้องกัน Overselling ได้ 100%

---

### ADR-003 — Use Synchronous gRPC for Order-to-Inventory Reservation
- **Status:** Accepted
- **Context:** ในระหว่างกระบวนการ Checkout ระบบต้องทราบผลการจองสต็อกทันทีแบบ Real-time ก่อนส่ง URL ชำระเงินให้ลูกค้า การสื่อสารด้วย Asynchronous จะทำให้เกิดสถานะก้ำกึ่งและต้องมีการชดเชยยกเลิกคำสั่งซื้อภายหลังมากเกินไป ขณะที่ REST ภายในมี overhead ด้าน Serialization สูง
- **Decision:** Order Service สื่อสารกับ Batch Inventory Service แบบ Synchronous ผ่าน gRPC (`contracts/proto/inventory/v1/inventory.proto`) สำหรับเมธอด `CheckAvailability`, `ReservePortions`, `ConfirmReservation`, และ `ReleaseReservation` โดยทุก Call ต้องมี Bounded Deadline และส่ง `Idempotency-Key` เสมอ
- **Consequences:** ทราบผลการจองสต็อกทันทีในระดับมิลลิวินาที สัญญา Interface ถูกบังคับอย่างเข้มงวดด้วย Protobuf

---

### ADR-004 — Use RabbitMQ for Non-blocking Domain Events
- **Status:** Accepted
- **Context:** หลังการชำระเงินเสร็จสิ้น มีกระบวนการต่อเนื่องหลายอย่าง เช่น การจัดคิวในครัว และการแจ้งเตือนลูกค้า ซึ่งไม่ควรนำมาหน่วงเวลา Response Time ของลูกค้า และไม่ควรทำให้การสั่งซื้อล้มเหลวหากระบบแจ้งเตือนมีปัญหาชั่วคราว
- **Decision:** ใช้ Asynchronous Domain Events ผ่าน RabbitMQ Broker โดย Service ต้นทาง (เช่น Order Service) บันทึก Event ลงใน Transactional Outbox ตารางเดียวกับข้อมูลทางธุรกิจ จากนั้น Outbox Dispatcher จะ Publish Event ในรูปแบบ **CloudEvents 1.0 JSON** ไปยัง Topic Exchanges ที่กำหนด พร้อมใช้ Publisher Confirms และ Mandatory Routing
- **Consequences:** ลูกค้าได้รับผลการสั่งซื้ออย่างรวดเร็ว ระบบ downstream แยกตัวเป็นอิสระ (Decoupled) และทนทานต่อความล้มเหลว (Fault-tolerant) ภายใต้โมเดล Eventual Consistency

---

### ADR-005 — Expose Client APIs through REST and an API Gateway
- **Status:** Accepted
- **Context:** ฝั่ง Client (Web Application ของลูกค้า, หน้าจอ Staff, หน้าจอ Manager) ต้องการการเชื่อมต่อที่เป็นมาตรฐานเดียว ไม่อิงกับ Topology ภายในของ Microservices
- **Decision:** ให้ Client ภายนอกติดต่อผ่าน HTTPS REST ที่ Versioned Path `/api/v1` โดยมี API Gateway ทำหน้าที่เป็นจุดเข้าใช้งานเดี่ยว (Single Entry Point) รับผิดชอบเรื่อง Route Forwarding, การตรวจสอบ JWT Token เบื้องต้น, การล้าง Spoofed Headers (`X-User-*`), และการตั้งค่า CORS Headers
- **Consequences:** Topology ภายในถูกซ่อนอย่างปลอดภัย Client ใช้งานผ่าน REST ที่เข้าใจง่ายตามมาตรฐานอุตสาหกรรม

---

### ADR-006 — Expose Catalog through REST and gRPC using Shared Application Logic
- **Status:** Accepted
- **Context:** Catalog Service ต้องให้บริการทั้ง REST API สำหรับหน้าเว็บแสดงเมนู และ gRPC API สำหรับ Order Service (ใช้ `BatchGetFlavors` เพื่อ Snapshot ราคาก่อนคิดเงิน) และ Inventory Service (ใช้ `GetFlavor` ตรวจสอบความถูกต้องก่อนเปิด Batch ผลิต)
- **Decision:** ให้ Catalog Service ให้บริการทั้ง HTTP REST (Port 3002) และ gRPC (Port 50052) ภายใน Process เดียวกัน โดยใช้ Application Core Logic และ Redis Repository เดียวกัน ทั้งสองช่องทางตรวจสอบสิทธิ์ด้วย JWT Token และจำกัดขนาด Payload (1 MiB)
- **Consequences:** ไม่เกิดการซ้ำซ้อนของโค้ดและการเก็บข้อมูล ให้บริการทั้งภายนอกและภายในได้อย่างมีประสิทธิภาพสูงสุด

---

### ADR-007 — Implement Inventory Transactions and Explicit Caller Boundaries
- **Status:** Accepted
- **Context:** การจองและตัดสต็อกเจลาโตต้องรองรับโหลดที่มีความแออัดสูง (Contention) เมื่อหลายคำสั่งซื้อสั่งรสชาติยอดนิยมพร้อมกันโดยไม่เกิด Deadlock หรือ Overselling
- **Decision:** บังคับใช้ Transaction-Scoped PostgreSQL Advisory Locks แยกตาม Flavor ID ร่วมกับ Row-level Locks โดยจัดลำดับการล็อคตาม Order Lock ก่อน แล้วตามด้วย Flavor Locks ที่เรียงลำดับอย่าง deterministic ก่อนทำการตัดสต็อกแบบ FEFO (First-Expired, First-Out) และใช้ Background Worker ทยอยตัดยอดสำหรับ Reservation ที่หมดอายุ (TTL 10 นาที)
- **Consequences:** รองรับการ Scale หลาย Container Replicas พร้อมกันได้อย่างปลอดภัยโดยไม่ต้องพึ่งพา Global Mutex ของแอปพลิเคชัน
